package main

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/hive/hivesim"
)

var (
	// parameters used for signing transactions
	chainID  = big.NewInt(7)
	gasPrice = big.NewInt(0 * params.GWei) // TODO: remove once we handle gas accounting

	// would be nice to use a networkID that's different from chainID,
	// but some clients don't support the distinction properly.
	networkID = big.NewInt(7)
)

var clientEnv = hivesim.Params{
	"HIVE_NODETYPE":       "full",
	"HIVE_NETWORK_ID":     networkID.String(),
	"HIVE_CHAIN_ID":       chainID.String(),
	"HIVE_FORK_HOMESTEAD": "0",
	"HIVE_FORK_TANGERINE": "0",
	"HIVE_FORK_SPURIOUS":  "0",

	// All tests use clique PoA to mine new blocks.
	"HIVE_CLIQUE_PERIOD":                    "1",
	"HIVE_CLIQUE_PRIVATEKEY":                "9c647b8b7c4e7c3490668fb6c11473619db80c93704c70893d3813af4090c39c",
	"HIVE_MINER":                            "658bdf435d810c91414ec09147daa6db62406379",
	"HIVE_TERMINAL_TOTAL_DIFFICULTY_PASSED": "0",
}

var files = map[string]string{
	"/genesis.json": "./init/genesis.json",
}

type testSpec struct {
	Name  string
	About string
	Run   func(*TestEnv)
}

var tests = []testSpec{
	// HTTP RPC tests.
	{Name: "http/BalanceAndNonceAt", Run: balanceAndNonceAtTest},               // ok
	{Name: "http/CanonicalChain", Run: canonicalChainTest},                     // ok
	{Name: "http/CodeAt", Run: CodeAtTest},                                     // ok
	{Name: "http/ContractDeployment", Run: deployContractTest},                 // ok
	{Name: "http/ContractDeploymentOutOfGas", Run: deployContractOutOfGasTest}, // ok
	{Name: "http/EstimateGas", Run: estimateGasTest},                           // ok
	{Name: "http/GenesisBlockByHash", Run: genesisBlockByHashTest},             // ok
	{Name: "http/GenesisBlockByNumber", Run: genesisBlockByNumberTest},         // ok
	{Name: "http/GenesisHeaderByHash", Run: genesisHeaderByHashTest},           // ok
	{Name: "http/GenesisHeaderByNumber", Run: genesisHeaderByNumberTest},       // ok
	{Name: "http/Receipt", Run: receiptTest},                                   // ok
	{Name: "http/SyncProgress", Run: syncProgressTest},                         // ok
	{Name: "http/TransactionCount", Run: transactionCountTest},                 // ok
	{Name: "http/TransactionInBlock", Run: transactionInBlockTest},             // ok
	{Name: "http/TransactionReceipt", Run: TransactionReceiptTest},             // ok

	// // HTTP ABI tests.
	{Name: "http/ABICall", Run: callContractTest},         // ok
	{Name: "http/ABITransact", Run: transactContractTest}, // ok
	// Disable WS tests as it's not yet implemented for Kakarot RPC
	// // WebSocket RPC tests.
	// {Name: "ws/BalanceAndNonceAt", Run: balanceAndNonceAtTest},
	// {Name: "ws/CanonicalChain", Run: canonicalChainTest},
	// {Name: "ws/CodeAt", Run: CodeAtTest},
	// {Name: "ws/ContractDeployment", Run: deployContractTest},
	// {Name: "ws/ContractDeploymentOutOfGas", Run: deployContractOutOfGasTest},
	// {Name: "ws/EstimateGas", Run: estimateGasTest},
	// {Name: "ws/GenesisBlockByHash", Run: genesisBlockByHashTest},
	// {Name: "ws/GenesisBlockByNumber", Run: genesisBlockByNumberTest},
	// {Name: "ws/GenesisHeaderByHash", Run: genesisHeaderByHashTest},
	// {Name: "ws/GenesisHeaderByNumber", Run: genesisHeaderByNumberTest},
	// {Name: "ws/Receipt", Run: receiptTest},
	// {Name: "ws/SyncProgress", Run: syncProgressTest},
	// {Name: "ws/TransactionCount", Run: transactionCountTest},
	// {Name: "ws/TransactionInBlock", Run: transactionInBlockTest},
	// {Name: "ws/TransactionReceipt", Run: TransactionReceiptTest},

	// // WebSocket subscription tests.
	// {Name: "ws/NewHeadSubscription", Run: newHeadSubscriptionTest},
	// {Name: "ws/LogSubscription", Run: logSubscriptionTest},
	// {Name: "ws/TransactionInBlockSubscription", Run: transactionInBlockSubscriptionTest},

	// // WebSocket ABI tests.
	// {Name: "ws/ABICall", Run: callContractTest},
	// {Name: "ws/ABITransact", Run: transactContractTest},
}

func main() {
	suite := hivesim.Suite{
		Name: "rpc",
		Description: `
The RPC test suite runs a set of RPC related tests against a running node. It tests
several real-world scenarios such as sending value transactions, deploying a contract or
interacting with one.`[1:],
	}

	// Add tests for full nodes.
	suite.Add(&hivesim.ClientTestSpec{
		Role:        "eth1",
		Name:        "client launch",
		Description: `This test launches the client and collects its logs.`,
		Parameters:  clientEnv,
		Files:       files,
		Run:         func(t *hivesim.T, c *hivesim.Client) { runAllTests(t, c, c.Type) },
		AlwaysRun:   true,
	})

	sim := hivesim.New()
	hivesim.MustRunSuite(sim, suite)
}

func runLESTests(t *hivesim.T, serverNode *hivesim.Client) {
	// Configure LES client.
	clientParams := clientEnv.Set("HIVE_NODETYPE", "light")
	// Disable mining.
	clientParams = clientParams.Set("HIVE_MINER", "")
	clientParams = clientParams.Set("HIVE_CLIQUE_PRIVATEKEY", "")

	enode, err := serverNode.EnodeURL()
	if err != nil {
		t.Fatal("can't get node peer-to-peer endpoint:", enode)
	}

	// Sync all sink nodes against the source.
	t.RunAllClients(hivesim.ClientTestSpec{
		Role:        "eth1_les_client",
		Name:        "CLIENT as LES client",
		Description: "This runs the RPC tests against an LES client.",
		Parameters:  clientParams,
		Files:       files,
		AlwaysRun:   true,
		Run: func(t *hivesim.T, client *hivesim.Client) {
			err := client.RPC().Call(nil, "admin_addPeer", enode)
			if err != nil {
				t.Fatalf("connection failed:", err)
			}
			waitSynced(client.RPC())
			runAllTests(t, client, client.Type+" LES")
		},
	})
}

// runAllTests runs the tests against a client instance.
// Most tests simply wait for tx inclusion in a block so we can run many tests concurrently.
func runAllTests(t *hivesim.T, c *hivesim.Client, clientName string) {
	vault := newVault()

	for _, test := range tests {
		test := test
		t.Run(hivesim.TestSpec{
			Name:        fmt.Sprintf("%s (%s)", test.Name, clientName),
			Description: test.About,
			Run: func(t *hivesim.T) {
				switch test.Name[:strings.IndexByte(test.Name, '/')] {
				case "http":
					runHTTP(t, c, vault, test.Run)
				case "ws":
					runWS(t, c, vault, test.Run)
				default:
					panic("bad test prefix in name " + test.Name)
				}
			},
		})
	}
}

type semaphore chan struct{}

func newSemaphore(n int) semaphore {
	s := make(semaphore, n)
	for i := 0; i < n; i++ {
		s <- struct{}{}
	}
	return s
}

func (s semaphore) get() { <-s }
func (s semaphore) put() { s <- struct{}{} }

func (s semaphore) drain() {
	for i := 0; i < cap(s); i++ {
		<-s
	}
}
