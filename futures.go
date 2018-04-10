package zbft

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/hexablock/blockchain/bcpb"
	"github.com/hexablock/hasher"
	"github.com/hexablock/log"
)

var errTimedOut = errors.New("timed out")

// Future represents a set of transactions that are undergoing ratification.
// These have not yet been applied and/or executed. It is used to signal
// completion of ratification and execution.
type Future struct {
	txs []*bcpb.Tx

	root bcpb.Digest

	ratified chan error
	execd    chan error
}

func newFuture(root bcpb.Digest) *Future {
	return &Future{
		root:     root,
		ratified: make(chan error, 1),
		execd:    make(chan error, 1),
	}
}

// Txs returns the transactions for the future.  The return will be nil if there
// is an error or preparation has not completed.  The returned transactions
// should be considered valid only after ratification has completed.
func (a *Future) Txs() []*bcpb.Tx {
	return a.txs
}

// Ratified block until ratification is complete or time out occurs returning
// the ratification error or timeout err or if any
func (a *Future) Ratified(timeout time.Duration) error {
	var (
		tmr = time.NewTimer(timeout)
		err error
	)

	select {
	case err = <-a.ratified:
	case <-tmr.C:
		err = errTimedOut
	}

	return err
}

// Executed until execution completes or timeout is reached.
func (a *Future) Executed(timeout time.Duration) error {
	var (
		tmr = time.NewTimer(timeout)
		err error
	)

	select {
	case err = <-a.execd:
	case <-tmr.C:
		err = errTimedOut
	}

	return err
}

// Wait blocks until both ratification and execution complete whether they
// succeed or fail
func (a *Future) Wait(timeout time.Duration) error {
	var (
		tmr = time.NewTimer(timeout)
		err error
	)

	select {
	case err = <-a.ratified:
		if err != nil {
			break
		}

		select {
		case err = <-a.execd:
		case <-tmr.C:
			err = errTimedOut
		}

	case <-tmr.C:
		err = errTimedOut
	}

	return err
}

func (a *Future) setRatified(err error) {
	a.ratified <- err
	close(a.ratified)
}

func (a *Future) setExecd(err error) {
	a.execd <- err
	close(a.execd)
}

// copy and set transactions
func (a *Future) setTxs(txs []*bcpb.Tx) {
	a.txs = make([]*bcpb.Tx, len(txs))
	copy(a.txs, txs)
}

// futures holds all active transactions
type futures struct {
	h hasher.Hasher

	log *log.Logger

	mu  sync.RWMutex
	fut map[string]*Future
}

func newFutures(h hasher.Hasher, logger *log.Logger) *futures {
	return &futures{
		h:   h,
		log: logger,
		fut: make(map[string]*Future),
	}
}

// Track transactions
func (f *futures) addTxsActive(txs []*bcpb.Tx) *Future {

	root := f.txInputsRoot(txs)
	active := newFuture(root)

	f.mu.Lock()
	f.fut[root.String()] = active
	f.mu.Unlock()

	f.log.Debugf("Added future=%s count=%d", root, len(txs))
	return active
}

// setTxsRatified marks the txs as ratified.  It removes them if the error is
// non-nil
func (f *futures) setTxsRatified(root bcpb.Digest, err error) {
	r := root.String()

	if err != nil {

		f.mu.Lock()
		if v, ok := f.fut[r]; ok {
			v.setRatified(err)
			delete(f.fut, r)
			f.log.Debugf("Removed future=%s error='%v'", r, err)
		}
		f.mu.Unlock()

	} else {

		f.mu.RLock()
		if f, ok := f.fut[r]; ok {
			f.setRatified(err)
		}
		f.mu.RUnlock()

	}

}

// setTxsExec marks the txs as executed and stops tracking them ie. removes them
// from the map
func (f *futures) setTxsExec(root bcpb.Digest, err error) {
	r := root.String()
	f.mu.Lock()
	if v, ok := f.fut[r]; ok {
		v.setExecd(err)
		delete(f.fut, r)
		f.log.Debugf("Removed future=%s", r)
	}
	f.mu.Unlock()
}

func (f *futures) txInputsRoot(txs []*bcpb.Tx) bcpb.Digest {

	h := f.h.New()
	for i := range txs {
		// Write tx timestamp
		binary.Write(h, binary.BigEndian, txs[i].Header.Timestamp)

		// Write each input hash
		for j := range txs[i].Inputs {
			sh := txs[i].Inputs[j].Hash(f.h)
			h.Write(sh)
		}

	}

	sh := h.Sum(nil)
	return bcpb.NewDigest(f.h.Name(), sh)
}

func (f *futures) setTxs(root bcpb.Digest, txs []*bcpb.Tx) error {
	var (
		r   = root.String()
		err error
	)

	f.mu.Lock()
	if v, ok := f.fut[r]; ok {
		v.setTxs(txs)
	} else {
		err = errors.New("tx input root not found")
	}
	f.mu.Unlock()

	return err
}
