package zbft

import (
	"fmt"

	"github.com/hexablock/blockchain/bcpb"
	"github.com/hexablock/zbft/zbftpb"
)

func (z *zbft) handleBootstrap(msg zbftpb.Message) error {
	if z.inst.state != stateInit {
		return fmt.Errorf("cannot bootstrap in state=%v", z.inst.state)
	}
	// Make sure the genesis block hasn't already been set.  This will we
	// checked again when we try to persist and is here to avoid a round of
	// failed voting for the genesis block
	if z.bc.Genesis() != nil {
		return fmt.Errorf("already bootstrapped: genesis block exists")
	}

	z.inst.onCommitEnter = z.onCommitEnterBootstrap
	z.inst.onRatified = z.onRatifiedBootstrap

	z.log.Debug("Bootstrapping...")

	// Instantiate voting instance
	z.initRound(msg.Block, msg.Txs)

	// Self sign
	signature, err := z.kp.Sign(msg.Block.Digest)
	if err == nil {
		pubkey := z.kp.PublicKey
		err = z.inst.sign(pubkey, signature)
	}

	return err
}

// called when we enter commit when bootstrapping
func (z *zbft) onCommitEnterBootstrap(blk *bcpb.Block, txs []*bcpb.Tx) error {
	err := z.bc.SetGenesis(blk, txs)
	if err != nil {
		z.resetRound()
		return err
	}

	return z.voteCommitAndBroadcast(blk, txs)
}

// called when genesis block is ratified
func (z *zbft) onRatifiedBootstrap(blk *bcpb.Block, txs []*bcpb.Tx) error {
	err := z.onRatified(blk, txs)

	z.inst.onCommitEnter = z.onCommitEnter
	z.inst.onRatified = z.onRatified

	return err
}
