package zbft

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/hexablock/blockchain"
	"github.com/hexablock/blockchain/bcpb"
	"github.com/hexablock/blockchain/keypair"
	"github.com/hexablock/hasher"
	"github.com/hexablock/log"
	"github.com/hexablock/zbft/zbftpb"
)

var (
	errUnknownMessageType = errors.New("unknown message type")
	errInvalidBlockDigest = errors.New("invalid block digest")
)

const (
	defaultRoundTimeout = 3 * time.Second
)

const (
	confChangeTimeout = iota + 1
)

type execBlock struct {
	leader bool
	block  *bcpb.Block
	txs    []*bcpb.Tx
}

type configChange struct {
	typ  uint8
	data interface{}
}

type zbft struct {
	// Blockchain
	bc *blockchain.Blockchain

	// Reference obtained from the blockchain
	hasher hasher.Hasher

	kp *keypair.KeyPair

	// Current active block being ratified
	inst *instance
	// Timer for current active instance
	timer *time.Timer
	// Timer timeout for round
	roundTimeout time.Duration

	// Channel with messages to be broadcasted to network
	msgBcast chan zbftpb.Message

	// Incoming messages.  These can be requests or consensus messages
	msgIn chan zbftpb.Message

	log *log.Logger

	// Transactions that need to be prepared i.e. txos need to be produced
	// which are then passed to txCollect
	//txPrepare chan []*bcpb.Tx

	// Channel of txs ready to be added to a block.  These are txs from txPrepare
	// This is where txs are collected before being added to a block.
	txCollect chan []*bcpb.Tx

	// Channel to block and unblock transaction addition to blocks based on
	// current consensus state.  This is used to start and stop tx collection
	// by setting to txCollect or nil
	txq chan []*bcpb.Tx

	// Contract library
	fsm FSM

	// Blocks and associated txs available to execute
	exec chan *execBlock

	confCh chan configChange

	// All active transactions
	futs *futures
}

func (z *zbft) init() {
	// Enable tx queue
	z.txq = z.txCollect

	// Init futures
	z.futs = newFutures(z.hasher, z.log)

	// Voting instance
	inst := newInstance()
	inst.onSignEnter = z.onSignEnter
	inst.onCommitEnter = z.onCommitEnter
	inst.onRatified = z.onRatified
	z.inst = inst

	// Timer setup
	z.timer = time.NewTimer(z.roundTimeout)
	z.timer.Stop()
}

func (z *zbft) startExecing() {
	var err error
	for eb := range z.exec {

		// The error is bubbled up via the future
		err = z.fsm.Execute(eb.txs, eb.block.Header, eb.leader)
		if eb.leader {
			root := z.futs.txInputsRoot(eb.txs)
			z.futs.setTxsExec(root, err)
		}

		err = z.bc.SetLastExec(eb.block.Digest)
		if err != nil {
			z.log.Errorf("Failed to mark last executed: %v", err)
		}

	}

}

// take the collected transactions and submit a block proposal
func (z *zbft) handleReadyTxs(txs []*bcpb.Tx) {
	last := z.bc.Last()

	// New block
	next := bcpb.NewBlock()
	next.Header.PrevBlock = last.Digest
	next.Header.Height = last.Height() + 1
	next.Header.Nonce = last.Header.Nonce + 1

	// TODO: change
	next.Header.N = last.Header.N
	next.Header.S = last.Header.S
	next.Header.Q = last.Header.Q
	next.SetSigners(last.Header.Signers...)
	next.SetProposer(z.kp.PublicKey)

	pmsg := zbftpb.Message{
		Type:  zbftpb.Message_PROPOSAL,
		Block: next,
		Txs:   txs,
		From:  z.kp.PublicKey,
	}

	z.msgIn <- pmsg
	z.broadcast(pmsg)

	runtime.Gosched()
}

func (z *zbft) handleMessage(msg zbftpb.Message) error {
	var err error

	switch msg.Type {

	case zbftpb.Message_BOOTSTRAP:
		err = z.handleBootstrap(msg)

	case zbftpb.Message_PROPOSAL:
		err = z.handleProposal(msg)

	case zbftpb.Message_SIGNATURE:
		err = z.handleSignature(msg)

	case zbftpb.Message_PERSIST:
		err = z.handlePersist(msg)

	default:
		err = errUnknownMessageType

	}

	if err != nil {
		z.log.Errorf("Message %v: %v", msg.Type, err)
	}

	return err
}

func (z *zbft) handleProposal(msg zbftpb.Message) error {
	if z.inst.state != stateInit {
		return fmt.Errorf("cannot propose block in state=%v", z.inst.state)
	}

	// Check if its the next in line
	last := z.bc.Last()
	if !msg.Block.Header.PrevBlock.Equal(last.Digest) {
		return blockchain.ErrPrevBlockMismatch
	}

	if err := z.fsm.Prepare(msg.Txs); err != nil {
		root := z.futs.txInputsRoot(msg.Txs)
		z.futs.setTxsRatified(root, err)
		return err
	}

	// Instantiate voting instance
	z.initRound(msg.Block, msg.Txs)

	// Self sign
	signature, err := z.kp.Sign(msg.Block.Digest)
	if err == nil {
		pubkey := z.kp.PublicKey
		err = z.inst.sign(msg.Block.Digest, pubkey, signature)
	}

	return err
}

func (z *zbft) handleSignature(msg zbftpb.Message) error {
	if z.inst.state < stateSigning {
		return fmt.Errorf("cannot sign block in state=%v", z.inst.state)
	}

	// Make sure the same block is being ratified as the one in the request
	blk := msg.Block
	digest := blk.Digest

	if blk.Header == nil {
		return errors.New("block header missing")
	}

	if len(blk.Header.Signers) != 1 {
		return fmt.Errorf("signers missing")
	}
	signer := blk.Header.Signers[0]

	if len(blk.Signatures) != 1 {
		return fmt.Errorf("no signatures in block")
	}
	signature := blk.Signatures[0]

	err := z.inst.sign(digest, signer, signature)

	z.log.Debugf("[%x] Signed: signer=%x sig=%x err='%v'",
		z.kp.PublicKey[:8], signer[:8], signature[:8], err)

	return err
}

func (z *zbft) handlePersist(msg zbftpb.Message) error {
	blk := msg.Block
	if blk.Digest.Equal(z.inst.block.Digest) {
		// TODO: close future
		return z.inst.commit(msg.From)
	}
	return errInvalidBlockDigest
}

func (z *zbft) handleErrorAndReset(err error) {
	if z.isRoundLeader() {
		root := z.futs.txInputsRoot(z.inst.txs)
		z.futs.setTxsRatified(root, err)
	}
	z.resetRound()
}

func (z *zbft) handleConfigChange(cch configChange) {
	if z.inst.state != stateInit {
		z.log.Debugf("Rescheduling config change state=%s type=%d", z.inst.state, cch.typ)
		z.confCh <- cch
		return
	}

	switch cch.typ {

	case confChangeTimeout:
		z.handleSetTimeout(cch.data)

	default:
		z.log.Errorf("Unknown config change type: %d", cch.typ)

	}
}

func (z *zbft) handleSetTimeout(data interface{}) {
	d, ok := data.(time.Duration)
	if ok {
		z.roundTimeout = d
		return
	}

	z.log.Errorf("Wrong timeout type: %v", data)
}

// called when the node goes into signing stage.  Used by bootstrap as well
func (z *zbft) onSignEnter(digest bcpb.Digest, pk bcpb.PublicKey, signature []byte) {
	// Broadcast we've signed with our signature
	z.broadcast(zbftpb.Message{
		Type: zbftpb.Message_SIGNATURE,
		Block: &bcpb.Block{
			Digest: digest,
			Header: &bcpb.BlockHeader{
				Signers: []bcpb.PublicKey{pk},
			},
			Signatures: [][]byte{signature},
		},
	})
}

// persist block and broadcast
func (z *zbft) onCommitEnter(blk *bcpb.Block, txs []*bcpb.Tx) error {
	z.log.Debugf("Committing: %s", blk.Digest)

	// Persist the block but do not update the last block reference yet
	_, err := z.bc.Append(blk, txs)

	switch err {

	case nil:
		err = z.voteCommitAndBroadcast(blk, txs)

	case bcpb.ErrSignerNotInBlock, bcpb.ErrSignerAlreadySigned:
		// We ignore the above 2 errors as these should not interfere
		// with the voting process

	default:
		// TODO: close
		z.handleErrorAndReset(err)
	}

	return err
}

func (z *zbft) isRoundLeader() bool {
	return z.kp.PublicKey.Equal(z.inst.block.Header.Proposer())
}

// Block is persisted.  Commit the last block in the ledger
func (z *zbft) onRatified(blk *bcpb.Block, txs []*bcpb.Tx) error {
	z.log.Debugf("Ratified: %v", blk.Digest)

	// Update last block reference
	err := z.bc.Commit(blk.Digest)

	// Are we the leader
	leader := z.kp.PublicKey.Equal(blk.Header.Proposer())

	// Update future if we're the round leader
	if leader {
		root := z.futs.txInputsRoot(txs)
		z.futs.setTxsRatified(root, err)
	}

	// Only submit for execution if commit succeeds
	if err == nil {
		eb := &execBlock{leader, blk, txs}
		z.exec <- eb
	}

	// Reset state
	z.resetRound()

	return err
}

// initRound is called to initialize a new votinging round
func (z *zbft) initRound(blk *bcpb.Block, txs []*bcpb.Tx) {
	// Disable transaction q.  Cause channel to block
	z.txq = nil

	// Compute based on what was provided.
	blk.SetTxs(txs, z.hasher)
	blk.SetHash(z.hasher)

	// Instantiate voting instance
	z.inst.init(blk, txs)

	// Start timer for the round
	z.timer.Reset(z.roundTimeout)
}

func (z *zbft) resetRound() {
	// Reset consensus
	z.inst.reset()

	// stop timeout timer
	z.timer.Stop()

	// Enable transaction q.  Unblocks assuming new txs are available
	z.txq = z.txCollect
}

func (z *zbft) voteCommitAndBroadcast(blk *bcpb.Block, txs []*bcpb.Tx) error {
	// Mark we have committed to disk
	err := z.inst.commit(z.kp.PublicKey)
	if err == nil {

		z.log.Debugf("Committed: %v", blk.Digest)
		// Broadcast that we have persisted
		z.broadcast(zbftpb.Message{
			Type:  zbftpb.Message_PERSIST,
			Block: blk,
			Txs:   txs,
			From:  z.kp.PublicKey,
		})

	}

	return err
}

func (z *zbft) broadcast(msg zbftpb.Message) {
	z.msgBcast <- msg
}
