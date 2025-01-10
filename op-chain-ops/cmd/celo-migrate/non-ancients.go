package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
)

func copyDbExceptAncients(oldDbPath, newDbPath string) error {
	defer timer("copyDbExceptAncients")()

	log.Info("Copying files from old database (excluding ancients)", "process", "non-ancients")

	// Get rsync help output
	cmdHelp := exec.Command("rsync", "--help")
	output, err := cmdHelp.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get rsync help output: %w", err)
	}

	// Convert output to string
	outputStr := string(output)

	opts := []string{"-v", "-a", "--exclude=ancient", "--checksum", "--delete"}

	// Check for supported options
	// Prefer --info=progress2 over --progress
	if strings.Contains(outputStr, "--info") {
		opts = append(opts, "--info=progress2")
	} else if strings.Contains(outputStr, "--progress") {
		opts = append(opts, "--progress")
	}

	cmd := exec.Command("rsync", append(opts, oldDbPath+"/", newDbPath)...)

	// rsync copies any file with a different timestamp or size.
	//
	// '--exclude=ancient' excludes the ancient directory from the copy
	//
	// '--delete' Tells rsync to delete extraneous files from the receiving side (ones that aren’t on the sending side)
	//
	// '-a' archive mode; equals -rlptgoD. It is a quick way of saying you want recursion and want to preserve almost everything, including timestamps, ownerships, permissions, etc.
	// Timestamps are important here because they are used to determine which files are newer and should be copied over.
	//
	// '--whole-file' This is the default when both the source and destination are specified as local paths, which they are here (oldDbPath and newDbPath).
	// This option disables rsync’s delta-transfer algorithm, which causes all transferred files to be sent whole. The delta-transfer algorithm is normally used when the destination is a remote system.
	//
	// '--checksum' This forces rsync to compare the checksums of all files to determine if they are the same. This is slows down the transfer but ensures that source and destination directories end up with the same contents (excluding /ancients).

	log.Info("Running rsync command", "command", cmd.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to copy old database to new database: %w", err)
	}
	return nil
}

func migrateNonAncientsDb(newDB ethdb.Database, lastBlock, batchSize uint64, lastAncient *RLPBlockElement) (uint64, error) {
	defer timer("migrateNonAncientsDb")()

	// Delete bad blocks, we could migrate them, but we have no need for the historical bad blocks. AFAICS bad blocks
	// are stored solely so that they can be retrieved or traced via the debug API, but we are no longer interested
	// in these old bad blocks.
	rawdb.DeleteBadBlocks(newDB)

	if lastAncient != nil {
		// The genesis block is the only block that should remain stored in the non-ancient db even after it is frozen.
		log.Info("Migrating genesis block in non-ancient db", "process", "non-ancients")
		if err := migrateNonAncientBlocks(newDB, 0, 1, nil); err != nil {
			return 0, err
		}
	}

	prevBlockElement := *lastAncient
	for i := lastAncient.number + 1; i <= lastBlock; i += batchSize {
		if err := migrateNonAncientBlocks(newDB, i, batchSize, &prevBlockElement); err != nil {
			return 0, err
		}
	}

	migratedCount := lastBlock - lastAncient.number
	return migratedCount, nil
}

func migrateNonAncientBlocks(newDB ethdb.Database, start, count uint64, prevBlockElement *RLPBlockElement) error {
	log.Info("Processing Block Range", "process", "non-ancients", "from", start, "to(inclusve)", start+count-1, "count", count)

	blockRange, err := loadNonAncientRange(newDB, start, count)
	if err != nil {
		return err
	}
	if err = blockRange.CheckContinuity(prevBlockElement, count); err != nil {
		return fmt.Errorf("failed continuity check for non-ancient blocks: %w", err)
	}
	if err = blockRange.Transform(); err != nil {
		return err
	}
	if err = writeNonAncientBlockRange(newDB, blockRange); err != nil {
		return err
	}

	return nil
}

func loadNonAncientRange(newDB ethdb.Database, start, count uint64) (*RLPBlockRange, error) {
	blockRange := &RLPBlockRange{
		start:    start,
		hashes:   make([][]byte, count),
		headers:  make([][]byte, count),
		bodies:   make([][]byte, count),
		receipts: make([][]byte, count),
		tds:      make([][]byte, count),
	}
	numberHashes := rawdb.ReadAllHashesInRange(newDB, start, start+count-1) // minus 1 because start is included in count
	err := checkNumberHashes(newDB, numberHashes)
	if err != nil {
		return nil, err
	}
	for i, numberHash := range numberHashes {
		number := numberHash.Number
		hash := numberHash.Hash

		blockRange.hashes[i] = hash[:]
		blockRange.headers[i], err = newDB.Get(headerKey(number, hash))
		if err != nil {
			return nil, fmt.Errorf("failed to find header in newDB for non-ancient block %d - %x: %w", number, hash, err)
		}
		blockRange.bodies[i], err = newDB.Get(blockBodyKey(number, hash))
		if err != nil {
			return nil, fmt.Errorf("failed to find body in newDB for non-ancient block %d - %x: %w", number, hash, err)
		}
		blockRange.receipts[i], err = newDB.Get(blockReceiptsKey(number, hash))
		if err != nil {
			return nil, fmt.Errorf("failed to find receipts in newDB for non-ancient block %d - %x: %w", number, hash, err)
		}
		blockRange.tds[i], err = newDB.Get(headerTDKey(number, hash))
		if err != nil {
			return nil, fmt.Errorf("failed to find total difficulty in newDB for non-ancient block %d - %x: %w", number, hash, err)
		}
	}
	return blockRange, nil
}

// checkNumberHashes checks that the contents of a NumberHash slice match the contents in the headerNumber and headerHash db tables.
// We do this to account for any differences in the way NumberHashes are read from the db, and to ensure the slice only contains canonical data.
func checkNumberHashes(newDB ethdb.Database, numberHashes []*rawdb.NumberHash) error {
	for _, numberHash := range numberHashes {
		numberRLP, err := newDB.Get(headerNumberKey(numberHash.Hash))
		if err != nil {
			return fmt.Errorf("failed to find number for hash in newDB for non-ancient block %d - %x: %w", numberHash.Number, numberHash.Hash, err)
		}
		hashRLP, err := newDB.Get(headerHashKey(numberHash.Number))
		if err != nil {
			return fmt.Errorf("failed to find canonical hash in newDB for non-ancient block %d - %x: %w", numberHash.Number, numberHash.Hash, err)
		}
		if !bytes.Equal(hashRLP, numberHash.Hash[:]) {
			return fmt.Errorf("canonical hash mismatch in newDB for non-ancient block %d - %x: %w", numberHash.Number, numberHash.Hash, err)
		}
		if !bytes.Equal(numberRLP, encodeBlockNumber(numberHash.Number)) {
			return fmt.Errorf("number for hash mismatch in newDB for non-ancient block %d - %x: %w", numberHash.Number, numberHash.Hash, err)
		}
	}
	return nil
}

// write transformed header and body to newDB
func writeNonAncientBlock(newDB ethdb.Database, header, body, hashBytes []byte, number uint64) error {
	hash := common.BytesToHash(hashBytes)
	batch := newDB.NewBatch()
	rawdb.WriteBodyRLP(batch, hash, number, body)
	if err := batch.Put(headerKey(number, hash), header); err != nil {
		return fmt.Errorf("failed to write header: block %d - %x: %w", number, hash, err)
	}
	if err := batch.Write(); err != nil {
		return fmt.Errorf("failed to write header and body: block %d - %x: %w", number, hash, err)
	}

	return nil
}

func writeNonAncientBlockRange(newDB ethdb.Database, blockRange *RLPBlockRange) error {
	for i := range blockRange.hashes {
		if err := writeNonAncientBlock(newDB, blockRange.headers[i], blockRange.bodies[i], blockRange.hashes[i], blockRange.start+uint64(i)); err != nil {
			return err
		}
	}
	return nil
}
