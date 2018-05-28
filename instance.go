package zbft

import (
	"github.com/hexablock/blockchain/bcpb"
)

type state uint8

func (s state) String() string {
	switch s {

	case stateInit:
		return "INIT"

	case stateSigning:
		return "SIGNING"

	case stateCommitting:
		return "COMMITTING"

	case stateRatified:
		return "RATIFIED"

	default:
		return "UNKNOWN"

	}

}

const (
	stateInit state = iota + 3
	stateSigning
	stateCommitting
	stateRatified
)

type instance struct {
	state state

	// current block being ratified
	block *bcpb.Block

	// Actual transactions in the block
	txs []*bcpb.Tx

	// Received signer block commits
	commits []uint8

	// called when this node enters signing phase
	onSignEnter func(bcpb.Digest, bcpb.PublicKey, []byte)
	// called when this node begins the commit phase
	onCommitEnter func(*bcpb.Block, []*bcpb.Tx) error
	// called when this node ratifies
	onRatified func(*bcpb.Block, []*bcpb.Tx) error
}

func newInstance() *instance {
	inst := &instance{
		state: stateInit,
	}

	return inst
}

func (inst *instance) init(blk *bcpb.Block, txs []*bcpb.Tx) {
	inst.state = stateInit
	inst.block = blk
	inst.txs = txs

	inst.commits = make([]uint8, len(blk.Header.Signers))
}

func (inst *instance) sign(digest bcpb.Digest, pubkey bcpb.PublicKey, signature []byte) error {
	if !digest.Equal(inst.block.Digest) {
		return errInvalidBlockDigest
	}
	// Add public key and signature to the block
	err := inst.block.Sign(pubkey, signature)
	if err != nil {
		return err
	}

	// Check for state transition
	switch inst.state {

	case stateInit:
		// Transition into signing
		inst.state = stateSigning
		inst.onSignEnter(inst.block.Digest, pubkey, signature)

	case stateSigning:
		b := inst.block
		if b.HasSignatures() && b.ProposerSigned() {
			// Transition state only if we have signatures and the proposer has
			// signed
			inst.state = stateCommitting
			err = inst.onCommitEnter(b, inst.txs)
		}

	}

	return err
}

func (inst *instance) commit(pubkey bcpb.PublicKey) error {
	// Find signer index
	i := inst.block.Header.SignerIndex(pubkey)
	if i < 0 {
		return bcpb.ErrSignerNotInBlock
	}
	inst.commits[i] = 1

	// Check for state transition
	var err error

	switch inst.state {

	case stateCommitting:
		if inst.hasCommits() {
			inst.state = stateRatified
			err = inst.onRatified(inst.block, inst.txs)
		}

	}

	return err
}

func (inst *instance) reset() {
	inst.state = stateInit
	inst.block = nil
	inst.txs = nil
	inst.commits = nil
}

func (inst *instance) commitCount() int32 {
	var c uint8
	for _, v := range inst.commits {
		c += v
	}
	return int32(c)
}

func (inst *instance) hasCommits() bool {
	return inst.commitCount() == inst.block.Header.Q
}
