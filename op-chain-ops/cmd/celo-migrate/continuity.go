package main

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// RLPBlockRange is a range of blocks in RLP format
type RLPBlockRange struct {
	start    uint64
	hashes   [][]byte
	headers  [][]byte
	bodies   [][]byte
	receipts [][]byte
	tds      [][]byte
}

// RLPBlockElement contains all relevant block data in RLP format
type RLPBlockElement struct {
	decodedHeader *types.Header
	number        uint64
	hash          []byte
	header        []byte
	body          []byte
	receipts      []byte
	td            []byte
}

func (r *RLPBlockRange) Element(i uint64) (*RLPBlockElement, error) {
	header := types.Header{}
	err := rlp.DecodeBytes(r.headers[i], &header)
	if err != nil {
		return nil, fmt.Errorf("can't decode header: %w", err)
	}
	return &RLPBlockElement{
		decodedHeader: &header,
		number:        r.start + i, // TODO(Alec): how to use this?
		hash:          r.hashes[i],
		header:        r.headers[i],
		body:          r.bodies[i],
		receipts:      r.receipts[i],
		td:            r.tds[i],
	}, nil
}

func (r *RLPBlockRange) DropFirst() {
	r.start = r.start + 1
	r.hashes = r.hashes[1:]
	r.headers = r.headers[1:]
	r.bodies = r.bodies[1:]
	r.receipts = r.receipts[1:]
	r.tds = r.tds[1:]
}

// CheckContinuity checks if the block data in the range is continuous
// by comparing the header number and parent hash of each block with the previous block,
// and by checking if the number of elements retrieved from each table is the same.
// It takes in a pointer to the last element in the preceding range, and re-assigns it to
// the last element in the current range so that continuity can be checked across ranges.
func (r *RLPBlockRange) CheckContinuity(prevElement *RLPBlockElement) error {
	if err := r.CheckLengths(); err != nil {
		return err
	}
	for i := range r.hashes {
		currElement, err := r.Element(uint64(i))
		if err != nil {
			return err
		}
		if prevElement != nil {
			if err := currElement.Follows(prevElement); err != nil {
				return err
			}
		}
		prevElement = currElement
	}
	return nil
}

// CheckLengths makes sure the number of elements retrieved from each table is the same
func (r *RLPBlockRange) CheckLengths() error {
	var err error
	count := len(r.hashes)
	// TODO(Alec) should this take in an expected length parameter?
	// if len(r.hashes) != count {
	// 	err = fmt.Errorf("Expected count mismatch in block range hashes: expected %d, actual %d", count, len(r.hashes))
	// }
	if len(r.bodies) != count {
		err = errors.Join(err, fmt.Errorf("Expected count mismatch in block range bodies: expected %d, actual %d", count, len(r.bodies)))
	}
	if len(r.headers) != count {
		err = errors.Join(err, fmt.Errorf("Expected count mismatch in block range headers: expected %d, actual %d", count, len(r.headers)))
	}
	if len(r.receipts) != count {
		err = errors.Join(err, fmt.Errorf("Expected count mismatch in block range receipts: expected %d, actual %d", count, len(r.receipts)))
	}
	if len(r.tds) != count {
		err = errors.Join(err, fmt.Errorf("Expected count mismatch in block range total difficulties: expected %d, actual %d", count, len(r.tds)))
	}
	return err
}

// Transform transforms the necessary block data in the range
func (r *RLPBlockRange) Transform() error {
	for i := range r.hashes {
		blockNumber := r.start + uint64(i)

		newHeader, newBody, err := transform(r.headers[i], r.bodies[i], r.hashes[i], blockNumber)
		if err != nil {
			return err
		}

		r.headers[i] = newHeader
		r.bodies[i] = newBody
	}

	return nil
}

// Follows checks if the current block has a number one greater than the previous block
// and if the parent hash of the current block matches the hash of the previous block.
func (e *RLPBlockElement) Follows(prev *RLPBlockElement) error {
	if e.Header().Number.Uint64() != prev.Header().Number.Uint64()+1 {
		return fmt.Errorf("header number mismatch: expected %d, actual %d", prev.Header().Number.Uint64()+1, e.Header().Number.Uint64())
	}
	// We compare the parent hash with the stored hash of the previous block because
	// at this point the header object will not calculate the correct hash since it
	// first needs to be transformed.
	if e.Header().ParentHash != common.Hash(prev.hash) {
		return fmt.Errorf("parent hash mismatch between blocks %d and %d", e.Header().Number.Uint64(), prev.Header().Number.Uint64())
	}
	return nil
}

func (e *RLPBlockElement) Header() *types.Header {
	return e.decodedHeader
}
