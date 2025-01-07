package main

import (
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
	header        []byte // TODO(Alec): why this?
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

func (r *RLPBlockRange) CheckContinuity(prevElement *RLPBlockElement) error {
	for i := range r.hashes { // TODO(Alec): what if there are different lengths?
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
