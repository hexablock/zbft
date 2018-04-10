package zbft

import (
	"github.com/hexablock/blockchain"
	"github.com/hexablock/blockchain/bcpb"
	"github.com/hexablock/blockchain/keypair"
	"github.com/hexablock/log"

	"github.com/hexablock/zbft/zbftpb"
)

// TxStore implements a transaction store for read-only purposes
type TxStore interface {
	GetTXO(txi *bcpb.TxInput) (*bcpb.TxOutput, error)
}

// FSM implements a finite-state-machine used by zbft
type FSM interface {
	// Initializes the fsm with the given read-only tx store
	Init(store TxStore)
	// Prepare prepares the tx set writing the outputs back into the tx outputs
	Prepare(txs []*bcpb.Tx) error
	// Executes applies the given transactions
	Execute(txs []*bcpb.Tx, block *bcpb.BlockHeader, leader bool) error
}

// ZBFT is the interface used by the user to interact with the consensus
// alogrithm to perform ops against it
type ZBFT interface {
	// Start zbft
	Start()
	// SetGenesis is used to set the genesis block.  This can only be called
	// once for each ledger
	SetGenesis(blk *bcpb.Block, txs []*bcpb.Tx) *Future
	// Submits message to consensus algo
	Step(msg zbftpb.Message)
	// ProposeTxs proposes Transactions to the ledger.  They are first prepared,
	// added to a block then proposes to be added to the ledger
	ProposeTxs(txs []*bcpb.Tx) *Future
	// BroadcastMessages returns a read-only channel of messages that need to be
	// broadcasted to the network
	BroadcastMessages() <-chan zbftpb.Message
}

// New instantiates a new zbft instance. It takes a blockchain, finite-state-machine
// and a keypair as  arguments
func New(bc *blockchain.Blockchain, fsm FSM, kp *keypair.KeyPair, logger *log.Logger) ZBFT {
	z := &zbft{
		bc:        bc,
		hasher:    bc.Hasher().Clone(),
		kp:        kp,
		msgBcast:  make(chan zbftpb.Message, 16),
		msgIn:     make(chan zbftpb.Message, 16),
		txPrepare: make(chan []*bcpb.Tx, 16),
		txCollect: make(chan []*bcpb.Tx, 16),
		lib:       fsm,
		exec:      make(chan *execBlock, 16),
		log:       logger,
	}

	z.bc.SetBlockValidator(defaultBlockValidator)

	z.init()

	// Initialize contract library
	z.lib.Init(z.bc)

	return z
}

// SetGenesis broadcasts the given block to the network to bootstrap the ledger.
// Checking the existence of the previous block and the prepare phase are
// skipped for the genesis block
func (z *zbft) SetGenesis(blk *bcpb.Block, txs []*bcpb.Tx) *Future {
	fut := z.futs.addTxsActive(txs)

	msg := zbftpb.Message{
		Type:  zbftpb.Message_BOOTSTRAP,
		Block: blk,
		Txs:   txs,
		From:  z.kp.PublicKey,
	}

	z.msgIn <- msg
	z.broadcast(msg)

	return fut
}

// Step submits the message to the concensus engine
func (z *zbft) Step(msg zbftpb.Message) {
	z.msgIn <- msg
}

func (z *zbft) ProposeTxs(txs []*bcpb.Tx) *Future {
	fut := z.futs.addTxsActive(txs)
	z.txPrepare <- txs
	return fut
}

// BroadcastMessages returns a read-only channel of messages that need to be
// broadcasted to the network
func (z *zbft) BroadcastMessages() <-chan zbftpb.Message {
	return z.msgBcast
}

// Start starts the preparer, executor and concensus loops.  This is not started
// on initialization to allow loading of contract library before starting,
func (z *zbft) Start() {

	go z.startPreparing()
	go z.startExecing()

	for {

		select {

		case txs := <-z.txq:
			z.handleReadyTxs(txs)

		case msg := <-z.msgIn:
			z.handleMessage(msg)

		case <-z.timeout.C:
			z.handleTimeout()
		}

	}

}
