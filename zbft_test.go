package zbft

import (
	"fmt"

	"os"
	"testing"
	"time"

	"github.com/hexablock/blockchain"
	"github.com/hexablock/blockchain/bcpb"
	"github.com/hexablock/hasher"

	"github.com/hexablock/zbft/zbftpb"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	fmt.Println("Starting tests...")
	code := m.Run()
	os.Exit(code)
}

func Test_zbft_errors(t *testing.T) {
	tc := newTestZbft()
	go tc.Start()

	err := tc.handleMessage(zbftpb.Message{Type: 90})
	assert.Equal(t, errUnknownMessageType, err)

	err = tc.handleMessage(zbftpb.Message{Type: zbftpb.Message_SIGNATURE})
	assert.Contains(t, err.Error(), "cannot sign block")

	tc.inst.state = stateSigning
	err = tc.handleProposal(zbftpb.Message{Type: zbftpb.Message_PROPOSAL})
	assert.Contains(t, err.Error(), "cannot propose block")

	tc.inst.block = &bcpb.Block{Digest: []byte("fkfk")}

	tm := zbftpb.Message{
		Type:  zbftpb.Message_SIGNATURE,
		Block: &bcpb.Block{}}

	err = tc.handleMessage(tm)
	assert.Equal(t, errInvalidBlockDigest, err)

}

func Test_Zbft_Genesis(t *testing.T) {
	tc := newTestCluster(4)
	go tc.start()

	h := hasher.Default()

	tx := bcpb.NewBaseTx(tc.pubkeys()...)
	tx.AddOutput(&bcpb.TxOutput{DataKey: bcpb.DataKey("ledger:test")})
	txs := []*bcpb.Tx{tx}

	gnss := blockchain.NewGenesisBlock(h)

	gnss.Header.N = 4
	gnss.Header.S = 4
	gnss.Header.Q = 4
	gnss.SetSigners(tc.pubkeys()...)
	gnss.SetProposer(tc[0].kp.PublicKey)
	gnss.SetTxs(txs, h)
	gnss.SetHash(h)

	bfut := tc[0].SetGenesis(gnss, txs)
	assert.NotNil(t, bfut)

	//<-time.After(1 * time.Second)
	err := bfut.Executed(1 * time.Second)
	assert.Nil(t, err)

	// Check genesis
	for _, n := range tc {
		last := n.bc.Last()
		assert.Equal(t, gnss.Digest, last.Digest)
	}
}

func Test_Zbft_cluster(t *testing.T) {
	tc := newTestCluster(4)
	go tc.start()

	h := hasher.Default()

	tx := bcpb.NewBaseTx(tc.pubkeys()...)
	tx.AddOutput(&bcpb.TxOutput{DataKey: bcpb.DataKey("ledger:test")})
	txs := []*bcpb.Tx{tx}

	gnss := blockchain.NewGenesisBlock(h)

	gnss.Header.N = 4
	gnss.Header.S = 4
	gnss.Header.Q = 4
	gnss.SetSigners(tc.pubkeys()...)
	gnss.SetProposer(tc[0].kp.PublicKey)
	gnss.SetTxs(txs, h)
	gnss.SetHash(h)

	bfut := tc[0].SetGenesis(gnss, txs)
	assert.NotNil(t, bfut)

	//<-time.After(1 * time.Second)
	err := bfut.Executed(1 * time.Second)
	assert.Nil(t, err)

	// Check genesis
	for _, n := range tc {
		last := n.bc.Last()
		assert.Equal(t, gnss.Digest, last.Digest)
	}

	// Write 2 blocks
	for bcnt := 0; bcnt < 2; bcnt++ {

		txi := bcpb.NewTxInput(nil, -1, tc.pubkeys())
		txi.AddArgs(
			[]byte("dummy.create"),
			bcpb.DataKey(fmt.Sprintf("test:%d", bcnt)),
			[]byte(fmt.Sprintf("test:%d", bcnt)),
		)

		tx1 := bcpb.NewTx()
		tx1.AddInput(txi)
		//tx1 := bcpb.NewBaseTx(tc.pubkeys()...)
		//tx1.AddOutput(&bcpb.TxOutput{DataKey: bcpb.DataKey(fmt.Sprintf("test:%d", bcnt))})
		txs1 := []*bcpb.Tx{tx1}

		fut := tc[0].ProposeTxs(txs1)
		err = fut.Ratified(3 * time.Second)
		assert.Nil(t, err)

		for _, n := range tc {
			last := n.bc.Last()
			assert.NotEqual(t, gnss.Digest, last.Digest)
		}

	}

	// Update
	txi, err := tc[0].bc.NewTxInput(bcpb.DataKey("test:1"))
	assert.Nil(t, err)
	txi.AddArgs(
		[]byte("dummy.update"),
		[]byte("test:1-DATA"),
	)
	tx1 := bcpb.NewTx()
	tx1.AddInput(txi)

	fut := tc[0].ProposeTxs([]*bcpb.Tx{tx1})
	err = fut.Ratified(2 * time.Second)
	assert.Nil(t, err)

	// Check result
	rslt, err := tc[1].bc.GetTXOByDataKey(bcpb.DataKey("test:1"))
	assert.Nil(t, err)
	assert.Equal(t, []byte("test:1-DATA"), rslt.Data)

	// Check contract
	// r, _ := tc[2].cnct.m["test:1"]
	// assert.Equal(t, []byte("test:1-DATA"), r)

	// Check bootstrap
	emsg := zbftpb.Message{Type: zbftpb.Message_BOOTSTRAP}
	err = tc[0].handleBootstrap(emsg)
	assert.Contains(t, err.Error(), "already bootstrapped")

	timeout := 5 * time.Second
	tc[1].SetTimeout(timeout)
	<-time.After(300 * time.Millisecond)
	assert.Equal(t, timeout, tc[1].roundTimeout)
}
