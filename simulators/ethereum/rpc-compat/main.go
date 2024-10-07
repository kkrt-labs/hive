package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/hive/hivesim"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	diff "github.com/yudai/gojsondiff"
	"github.com/yudai/gojsondiff/formatter"
)

const EMPTY_ROOT = "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"

var (
	clientEnv = hivesim.Params{
		"HIVE_NODETYPE":       "full",
		"HIVE_NETWORK_ID":     "1337",
		"HIVE_CHAIN_ID":       "1337",
		"HIVE_FORK_HOMESTEAD": "0",
		//"HIVE_FORK_DAO_BLOCK":      2000,
		"HIVE_FORK_TANGERINE":                   "0",
		"HIVE_FORK_SPURIOUS":                    "0",
		"HIVE_FORK_BYZANTIUM":                   "0",
		"HIVE_FORK_CONSTANTINOPLE":              "0",
		"HIVE_FORK_PETERSBURG":                  "0",
		"HIVE_FORK_ISTANBUL":                    "0",
		"HIVE_FORK_BERLIN":                      "0",
		"HIVE_FORK_LONDON":                      "0",
		"HIVE_SHANGHAI_TIMESTAMP":               "0",
		"HIVE_TERMINAL_TOTAL_DIFFICULTY":        "0",
		"HIVE_TERMINAL_TOTAL_DIFFICULTY_PASSED": "1",
	}

	files = map[string]string{
		"genesis.json": "./tests/genesis.json",
		"chain.rlp":    "./tests/chain.rlp",
	}
)

func main() {
	// Run the test suite.
	suite := hivesim.Suite{
		Name: "rpc-compat",
		Description: `
The RPC-compatibility test suite runs a set of RPC related tests against a
running node. It tests client implementations of the JSON-RPC API for
conformance with the execution API specification.`[1:],
	}
	suite.Add(&hivesim.ClientTestSpec{
		Role:        "eth1",
		Name:        "client launch",
		Description: `This test launches the client and collects its logs.`,
		Parameters:  clientEnv,
		Files:       files,
		Run: func(t *hivesim.T, c *hivesim.Client) {
			runAllTests(t, c, c.Type)
		},
		AlwaysRun: true,
	})
	sim := hivesim.New()
	hivesim.MustRunSuite(sim, suite)
}

func runAllTests(t *hivesim.T, c *hivesim.Client, clientName string) {
	_, testPattern := t.Sim.TestPattern()
	re := regexp.MustCompile(testPattern)
	tests := loadTests(t, "tests", re)
	for _, test := range tests {
		test := test
		t.Run(hivesim.TestSpec{
			Name:        test.name,
			Description: test.comment,
			Run: func(t *hivesim.T) {
				if err := runTest(t, c, &test); err != nil {
					t.Fatal(err)
				}
			},
		})
	}
}

func runTest(t *hivesim.T, c *hivesim.Client, test *rpcTest) error {
	var (
		client    = &http.Client{Timeout: 5 * time.Second}
		url       = fmt.Sprintf("http://%s", net.JoinHostPort(c.IP.String(), "8545"))
		err       error
		respBytes []byte
	)

	for _, msg := range test.messages {
		if msg.send {
			// Send request.
			t.Log(">> ", msg.data)
			respBytes, err = postHttp(client, url, strings.NewReader(msg.data))
			if err != nil {
				return err
			}
		} else {
			// Receive a response.
			if respBytes == nil {
				return fmt.Errorf("invalid test, response before request")
			}
			expectedData := msg.data
			resp := string(bytes.TrimSpace(respBytes))
			t.Log("<< ", resp)
			if !gjson.Valid(resp) {
				return fmt.Errorf("invalid JSON response")
			}

			// Patch JSON to remove error messages. We only do this in the specific case
			// where an error is expected AND returned by the client.
			var errorRedacted bool
			if gjson.Get(resp, "error").Exists() && gjson.Get(expectedData, "error").Exists() {
				resp, _ = sjson.Delete(resp, "error.message")
				expectedData, _ = sjson.Delete(expectedData, "error.message")
				errorRedacted = true
			}

			// Clean the response and expected data for Kakarot
			resp, expectedData = cleanKakarot(resp, expectedData)

			// Compare responses.
			d, err := diff.New().Compare([]byte(resp), []byte(expectedData))
			if err != nil {
				return fmt.Errorf("failed to unmarshal value: %s\n", err)
			}

			// If there is a discrepancy, return error.
			if d.Modified() {
				if errorRedacted {
					t.Log("note: error messages removed from comparison")
				}
				var got map[string]any
				json.Unmarshal([]byte(resp), &got)
				config := formatter.AsciiFormatterConfig{
					ShowArrayIndex: true,
					Coloring:       false,
				}
				formatter := formatter.NewAsciiFormatter(got, config)
				diffString, _ := formatter.Format(d)
				return fmt.Errorf("response differs from expected (-- client, ++ test):\n%s", diffString)
			}
			respBytes = nil
		}
	}

	if respBytes != nil {
		t.Fatalf("unhandled response in test case")
	}
	return nil
}

// sendHttp sends an HTTP POST with the provided json data and reads the
// response into a byte slice and returns it.
func postHttp(c *http.Client, url string, d io.Reader) ([]byte, error) {
	req, err := http.NewRequest("POST", url, d)
	if err != nil {
		return nil, fmt.Errorf("error building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("write error: %v", err)
	}
	return io.ReadAll(resp.Body)
}

// 🚧 WARNING KAKAROT
// cleanKakarot modifies the response and expected data in order
// to be compatible with Kakarot. This includes :
//   - removing any block hash field.
func cleanKakarot(resp, expectedData string) (string, string) {
	// We remove the data field from the error if it exists in both the response and expected data.
	// According to <https://www.jsonrpc.org/specification>, data is a reserved field for additional
	// information from the server on the error. It can be omitted. We remove it from the
	// response and expected data to make the comparison easier.
	if gjson.Get(resp, "error").Exists() && gjson.Get(expectedData, "error").Exists() {
		resp, _ = sjson.Delete(resp, "error.data")
		expectedData, _ = sjson.Delete(expectedData, "error.data")
	}

	resp, expectedData = cleanTransactionData(resp, expectedData)
	resp, expectedData = cleanReceiptData(resp, expectedData)

	resp, expectedData = cleanBlockData(resp, expectedData)

	resp, expectedData = cleanReceiptsData(resp, expectedData)

	return resp, expectedData
}

// 🚧 WARNING KAKAROT
// cleanBlockData modifies the response and expected data if the returned values match a block information.
func cleanBlockData(resp, expectedData string) (string, string) {
	// TODO: remove the miner, gasLimit and baseFeePerGas skip once we have a way to set these in Kakarot.
	var fields = []string{
		"result.hash",
		"result.parentHash",
		"result.timestamp",
		"result.baseFeePerGas",
		"result.difficulty",
		"result.gasLimit",
		"result.miner",
		"result.size",
		"result.stateRoot",
		"result.totalDifficulty",
		"result.withdrawals",
	}

	if expected := gjson.Get(expectedData, "result.withdrawalsRoot"); expected.Exists() {
		expectedRoot := expected.String()
		if expectedRoot != EMPTY_ROOT {
			fields = append(fields, "result.withdrawalsRoot")
		}
	}

	resp = deleteFields(resp, fields...)
	expectedData = deleteFields(expectedData, fields...)

	if gjson.Get(resp, "result.transactions").Exists() && gjson.Get(expectedData, "result.transactions").Exists() {
		transactions := gjson.Get(resp, "result.transactions").Array()
		for i := range transactions {
			resp = deleteField(resp, fmt.Sprintf("result.transactions.%d.blockHash", i))
		}

		transactions = gjson.Get(expectedData, "result.transactions").Array()
		for i := range transactions {
			expectedData = deleteField(expectedData, fmt.Sprintf("result.transactions.%d.blockHash", i))
		}
	}

	return resp, expectedData
}

// 🚧 WARNING KAKAROT
// cleanReceiptsData modifies the response and expected data if the returned values match a []receipt.
func cleanReceiptsData(resp, expectedData string) (string, string) {
	receipts := gjson.Get(resp, "result").Array()
	// TODO: remove the gas removal part once gas accounting is fixed in Kakarot
	for i := range receipts {
		var fields = []string{fmt.Sprintf("result.%d.blockHash", i), fmt.Sprintf("result.%d.cumulativeGasUsed", i), fmt.Sprintf("result.%d.gasUsed", i)}
		resp = deleteFields(resp, fields...)
	}

	receipts = gjson.Get(expectedData, "result").Array()
	for i := range receipts {
		var fields = []string{fmt.Sprintf("result.%d.blockHash", i), fmt.Sprintf("result.%d.cumulativeGasUsed", i), fmt.Sprintf("result.%d.gasUsed", i)}
		expectedData = deleteFields(expectedData, fields...)
	}

	return resp, expectedData
}

// 🚧 WARNING KAKAROT
// cleanTransactionData modifies the response and expected data if the returned values match a receipt.
func cleanTransactionData(resp, expectedData string) (string, string) {
	resp = deleteField(resp, "result.blockHash")
	expectedData = deleteField(expectedData, "result.blockHash")

	// Checksum transformation for 'from' and 'to' fields in the response
	// To be safe, we checksum both the response and expected data.
	fromResp := gjson.Get(resp, "result.from").String()
	toResp := gjson.Get(resp, "result.to").String()

	if fromResp != "" {
		checksumFrom := common.HexToAddress(fromResp).Hex()
		resp, _ = sjson.Set(resp, "result.from", checksumFrom)
	}

	if toResp != "" {
		checksumTo := common.HexToAddress(toResp).Hex()
		resp, _ = sjson.Set(resp, "result.to", checksumTo)
	}

	// Checksum transformation for 'from' and 'to' fields in the expected data
	// To be safe, we checksum both the response and expected data.
	fromExpected := gjson.Get(expectedData, "result.from").String()
	toExpected := gjson.Get(expectedData, "result.to").String()

	if fromExpected != "" {
		checksumFrom := common.HexToAddress(fromExpected).Hex()
		expectedData, _ = sjson.Set(expectedData, "result.from", checksumFrom)
	}

	if toExpected != "" {
		checksumTo := common.HexToAddress(toExpected).Hex()
		expectedData, _ = sjson.Set(expectedData, "result.to", checksumTo)
	}

	return resp, expectedData
}

// 🚧 WARNING KAKAROT
// cleanReceiptData modifies the response and expected data if the returned values match a receipt.
func cleanReceiptData(resp, expectedData string) (string, string) {
	if gjson.Get(resp, "result.logs").Exists() && gjson.Get(expectedData, "result.logs").Exists() {
		logs := gjson.Get(resp, "result.logs").Array()
		for i := range logs {
			resp = deleteField(resp, fmt.Sprintf("result.logs.%d.blockHash", i))
		}

		logs = gjson.Get(expectedData, "result.logs").Array()
		for i := range logs {
			expectedData = deleteField(expectedData, fmt.Sprintf("result.logs.%d.blockHash", i))
		}
	}

	return resp, expectedData
}

// deleteFields removes the fields from the JSON string.
func deleteFields(data string, fields ...string) string {
	for _, field := range fields {
		data = deleteField(data, field)
	}
	return data
}

// deleteField removes the field from the JSON string.
func deleteField(data, field string) string {
	if gjson.Get(data, field).Exists() {
		data, _ = sjson.Delete(data, field)
	}
	return data
}
