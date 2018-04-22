package zbft

// This file contains libs using for testing

import (
	"fmt"
	"io/ioutil"

	"github.com/dgraph-io/badger"
	"github.com/hexablock/blockchain"
	"github.com/hexablock/blockchain/bcpb"
	"github.com/hexablock/blockchain/keypair"
	"github.com/hexablock/blockchain/stores"
	"github.com/hexablock/hasher"
	"github.com/hexablock/log"

	"github.com/hexablock/zbft/zbftpb"
)

type config struct {
	*blockchain.Config
	FSM     FSM
	KeyPair *keypair.KeyPair
	Logger  *log.Logger
}

type dummyFSM struct {
	txstore TxStore
}

func (df *dummyFSM) Init(store TxStore) {
	df.txstore = store
}

func (df *dummyFSM) Prepare(txs []*bcpb.Tx) error {
	for i := range txs {
		for _, txi := range txs[i].Inputs {
			var (
				newTxo *bcpb.TxOutput
				args   = txi.Args()
			)
			if txi.IsBase() {

				newTxo = &bcpb.TxOutput{
					DataKey: bcpb.DataKey(args[1]),
					Data:    make([]byte, len(args[2])),
				}
				copy(newTxo.Data, args[2])

			} else {

				currTxo, err := df.txstore.GetTXO(txi)
				if err != nil {
					return err
				}

				newTxo = &bcpb.TxOutput{
					DataKey: currTxo.DataKey,
					Data:    make([]byte, len(args[1])),
				}
				copy(newTxo.Data, args[1])
			}

			txs[i].AddOutput(newTxo)
		}
	}

	return nil
}

func (df *dummyFSM) Execute(txs []*bcpb.Tx, block *bcpb.BlockHeader, leader bool) error {
	fmt.Println("EXECED")
	return nil
}

func testBadgerDB(tmpdir string) (*badger.DB, error) {
	opt := badger.DefaultOptions
	opt.Dir = tmpdir
	opt.ValueDir = tmpdir
	return badger.Open(opt)
}

func testConf(db *badger.DB, prefix string) *config {
	conf := &config{
		Config: blockchain.DefaultConfig(),
		Logger: log.NewDefaultLogger(),
	}
	conf.Logger.EnableDebug(true)

	conf.FSM = &dummyFSM{}

	conf.BlockStorage = stores.NewBadgerBlockStorage(db, []byte(prefix), conf.Hasher)
	conf.TxStorage = stores.NewBadgerTxStorage(db, []byte(prefix))
	conf.DataKeyIndex = stores.NewBadgerDataKeyIndex(db, []byte(prefix))
	conf.KeyPair, _ = keypair.Generate(conf.Curve, hasher.Default())

	return conf
}

type testZbft struct {
	dir string
	//cnct *testContract
	*zbft
}

func newTestZbft() *testZbft {

	tmpdir, _ := ioutil.TempDir("/tmp", "zbft")
	db, err := testBadgerDB(tmpdir)
	if err != nil {
		panic(err)
	}

	conf := testConf(db, "zbft-test/")

	bc := blockchain.New(conf.Config)
	zconf := &Config{
		conf.KeyPair,
		bc,
		conf.FSM,
		conf.Logger,
	}
	z := New(zconf)

	return &testZbft{tmpdir, z.(*zbft)}
}

type testCluster []*testZbft

func newTestCluster(c int) testCluster {
	nodes := make([]*testZbft, c)
	for i := 0; i < c; i++ {
		nodes[i] = newTestZbft()
	}
	return testCluster(nodes)
}

func (tc testCluster) bcast(msg zbftpb.Message, j int) {
	for i := range tc {
		if i == j {
			continue
		}

		tc[i].Step(cloneMsg(msg))
	}
}

func (tc testCluster) start() {
	for i := range tc {
		go tc[i].Start()
	}

	for {

		select {
		case msg := <-tc[0].BroadcastMessages():
			tc.bcast(msg, 0)
		case msg := <-tc[1].BroadcastMessages():
			tc.bcast(msg, 1)
		case msg := <-tc[2].BroadcastMessages():
			tc.bcast(msg, 2)
		case msg := <-tc[3].BroadcastMessages():
			tc.bcast(msg, 3)
			// case msg := <-tc[4].bcast:
			// 	tc.bcast(msg, 4)

		}
	}
}

func (tc testCluster) pubkeys() []bcpb.PublicKey {
	pubkeys := make([]bcpb.PublicKey, len(tc))
	for i := range tc {
		pubkeys[i] = tc[i].kp.PublicKey
	}
	return pubkeys
}

func cloneMsg(msg zbftpb.Message) zbftpb.Message {
	nmsg := zbftpb.Message{
		Type:  msg.Type,
		Block: msg.Block.Clone(),
		Txs:   make([]*bcpb.Tx, len(msg.Txs)),
		From:  msg.From,
	}
	copy(nmsg.Txs, msg.Txs)

	return nmsg
}
