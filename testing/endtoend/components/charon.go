package components

import (
	"context"
	"fmt"
	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/pkg/errors"
	"github.com/prysmaticlabs/prysm/config/params"
	"github.com/prysmaticlabs/prysm/io/file"
	"github.com/prysmaticlabs/prysm/testing/endtoend/helpers"
	e2e "github.com/prysmaticlabs/prysm/testing/endtoend/params"
	e2etypes "github.com/prysmaticlabs/prysm/testing/endtoend/types"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const charonPath = "/charon_test"

type CharonCluster struct {
	e2etypes.ComponentRunner
	config  *e2etypes.E2EConfig
	started chan struct{}
}

// NewCharonCluster creates and returns a charon cluster.
func NewCharonCluster(config *e2etypes.E2EConfig) *CharonCluster {
	return &CharonCluster{
		config:  config,
		started: make(chan struct{}, 1),
	}
}

// Start starts the configured amount of validators, also sending and mining their deposits.
func (s *CharonCluster) Start(ctx context.Context) error {
	err := s.copyFiles()
	if err != nil {
		panic(errors.Wrap(err, "charon copying files"))
	}

	// launch bootnode
	bootnodes := make([]e2etypes.ComponentRunner, 1)
	bootnodes[0] = NewCharonBootnode(s.config)
	go func() {
		err := bootnodes[0].Start(ctx)
		if err != nil {
			log.Error(errors.Wrap(err, "charon stopped bootnode"))
		}
	}()
	err = helpers.ComponentsStarted(ctx, bootnodes)
	if err != nil {
		panic(errors.Wrap(err, "charon starting bootnode"))
	}
	time.Sleep(500 * time.Millisecond)

	// Create charon nodes.
	charonNodeCount := e2e.TestParams.CharonNodeCount
	nodes := make([]e2etypes.ComponentRunner, charonNodeCount)
	for i := 0; i < charonNodeCount; i++ {
		nodes[i] = NewCharonNode(s.config, i)
	}
	go func() {
		err := helpers.WaitOnNodes(ctx, nodes, func() {
			//
		})
		if err != nil {
			log.Error(errors.Wrap(err, "charon stopped node"))
		}
	}()

	err = helpers.ComponentsStarted(ctx, nodes)
	if err != nil {
		panic(errors.Wrap(err, "charon starting nodes"))
	}
	time.Sleep(500 * time.Millisecond)

	vc := make([]e2etypes.ComponentRunner, charonNodeCount)
	for i := 0; i < charonNodeCount; i++ {
		vc[i] = NewCharonLHVCNode(s.config, i)
	}

	// Wait for all nodes to finish their job (blocking).
	// Once nodes are ready passed in handler function will be called.
	return helpers.WaitOnNodes(ctx, vc, func() {
		// All nodes stated, close channel, so that all services waiting on a set, can proceed.
		close(s.started)
	})
}

// Started checks whether charon node set is started and all nodes are ready to be queried.
func (s *CharonCluster) Started() <-chan struct{} {
	return s.started
}

func getCharonClusterPath() string {
	return e2e.TestParams.TestPath + charonPath
}

func (s *CharonCluster) copyFiles() error {
	dirs := []string{
		"cluster",
		"cluster/node0",
		"cluster/node1",
		"cluster/node2",
		"cluster/node3",
		"logs",
		"bootnode",
		"lh0/tn",
		"lh1/tn",
		"lh2/tn",
		"lh3/tn",
		"lh0/d",
		"lh1/d",
		"lh2/d",
		"lh3/d",
	}

	files := []string{
		"cluster/cluster-lock.json",
		"cluster/node0/keystore-0.json",
		"cluster/node0/keystore-0.txt",
		"cluster/node0/p2pkey",
		"cluster/node1/keystore-0.json",
		"cluster/node1/keystore-0.txt",
		"cluster/node1/p2pkey",
		"cluster/node2/keystore-0.json",
		"cluster/node2/keystore-0.txt",
		"cluster/node2/p2pkey",
		"cluster/node3/keystore-0.json",
		"cluster/node3/keystore-0.txt",
		"cluster/node3/p2pkey",
	}

	for _, dirName := range dirs {
		err := os.MkdirAll(path.Join(getCharonClusterPath(), dirName), fs.ModePerm)
		if err != nil {
			return err
		}
	}

	for _, fileName := range files {
		src, err := bazel.Runfile("third_party/charon/" + fileName)
		if err != nil {
			return err
		}
		fmt.Println(src)
		dst := path.Join(getCharonClusterPath(), fileName)

		_, err = copyFile(src, dst)
		if err != nil {
			return err
		}
	}

	return nil
}

// CharonNode represents a charon node.
type CharonNode struct {
	e2etypes.ComponentRunner
	config    *e2etypes.E2EConfig
	started   chan struct{}
	charonIdx int
	bootnode  bool
}

// NewCharonNode creates and returns a charon node.
func NewCharonNode(config *e2etypes.E2EConfig, charonIdx int) *CharonNode {
	return &CharonNode{
		config:    config,
		charonIdx: charonIdx,
		started:   make(chan struct{}, 1),
		bootnode:  false,
	}
}

func NewCharonBootnode(config *e2etypes.E2EConfig) *CharonNode {
	return &CharonNode{
		config:    config,
		charonIdx: math.MaxInt,
		started:   make(chan struct{}, 1),
		bootnode:  true,
	}
}

// Start starts a charon node.
func (v *CharonNode) Start(ctx context.Context) error {
	binaryPath, err := bazel.Runfile("third_party/charon/charon")
	if err != nil {
		return errors.Wrap(err, "charon binary not found")
	}

	_, err = bazel.Runfile("third_party/charon/cluster/cluster-lock.json")
	if err != nil {
		return errors.Wrap(err, "charon binary2 not found")
	}

	charonIdx := v.charonIdx

	args := v.getArgs()

	cmd := exec.CommandContext(ctx, binaryPath, args...)

	// Write stdout and stderr to log files.
	stdoutFile := path.Join(getCharonClusterPath(), "logs", fmt.Sprintf("charon_%d_stdout.log", charonIdx))
	stderrFile := path.Join(getCharonClusterPath(), "logs", fmt.Sprintf("charon_%d_stderr.log", charonIdx))
	if v.bootnode {
		stdoutFile = path.Join(getCharonClusterPath(), "logs", "charon_bootnode_stdout.log")
		stderrFile = path.Join(getCharonClusterPath(), "logs", "charon_bootnode_stderr.log")
	}

	stdout, err := os.Create(stdoutFile)
	if err != nil {
		return err
	}
	stderr, err := os.Create(stderrFile)
	if err != nil {
		return err
	}
	defer func() {
		if err := stdout.Close(); err != nil {
			log.WithError(err).Error("Failed to close stdout file")
		}
		if err := stderr.Close(); err != nil {
			log.WithError(err).Error("Failed to close stderr file")
		}
	}()
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	log.Infof("Starting charon node %d with flags: %s %s and output to %s %s", charonIdx, binaryPath, strings.Join(args, " "), stdoutFile, stderrFile)
	if err = cmd.Start(); err != nil {
		return err
	}

	close(v.started)

	return cmd.Wait()
}

// Started checks whether charon node is started and ready to be queried.
func (v *CharonNode) Started() <-chan struct{} {
	return v.started
}

func (v *CharonNode) getArgsRun() []string {
	charonIdx := v.charonIdx

	// we use the first beacon node
	beaconPort := e2e.TestParams.Ports.PrysmBeaconNodeGatewayPort

	return []string{
		"run",
		fmt.Sprintf("--%s=%s", "lock-file", path.Join(getCharonClusterPath(), "cluster", "cluster-lock.json")),
		fmt.Sprintf("--%s=http://localhost:%d", "beacon-node-endpoint", beaconPort),
		fmt.Sprintf("--%s=localhost:%d", "validator-api-address", e2e.TestParams.Ports.CharonGatewayPort+charonIdx),
		fmt.Sprintf("--%s=localhost:%d", "p2p-tcp-address", e2e.TestParams.Ports.CharonTCPPort+charonIdx),
		fmt.Sprintf("--%s=localhost:%d", "p2p-udp-address", e2e.TestParams.Ports.CharonUDPPort+charonIdx),
		fmt.Sprintf("--%s=http://127.0.0.1:%d/enr", "p2p-bootnodes", e2e.TestParams.Ports.CharonBootnodePort),
		fmt.Sprintf("--%s=%s", "data-dir", path.Join(getCharonClusterPath(), "cluster", fmt.Sprintf("node%d", charonIdx))),
		//	fmt.Sprintf("--%s=%s", "p2p-external-hostname", fmt.Sprintf("node%d", charonIdx)),
		fmt.Sprintf("--%s=%s", "log-level", "debug"),
	}
}

func (v *CharonNode) getArgsBootnode() []string {
	return []string{
		"bootnode",
		// fmt.Sprintf("--%s=%s", "lock-file", path.Join(getCharonClusterPath(), "cluster", "cluster-lock.json")),
		fmt.Sprintf("--%s=localhost:%d", "bootnode-http-address", e2e.TestParams.Ports.CharonBootnodePort),
		fmt.Sprintf("--%s=localhost:%d", "p2p-tcp-address", e2e.TestParams.Ports.CharonBootnodePort+1),
		fmt.Sprintf("--%s=localhost:%d", "p2p-udp-address", e2e.TestParams.Ports.CharonBootnodePort+2),
		fmt.Sprintf("--%s=%s", "data-dir", path.Join(getCharonClusterPath(), "bootnode")),
		fmt.Sprintf("--%s=%s", "log-level", "debug"),
	}
}

func (v *CharonNode) getArgs() []string {
	if v.bootnode {
		return v.getArgsBootnode()
	} else {
		return v.getArgsRun()
	}
}

type CharonLHVCNode struct {
	e2etypes.ComponentRunner
	config    *e2etypes.E2EConfig
	started   chan struct{}
	charonIdx int
}

// NewCharonLHVCNode creates and returns a validator node.
func NewCharonLHVCNode(config *e2etypes.E2EConfig, charonIdx int) *CharonLHVCNode {
	return &CharonLHVCNode{
		config:    config,
		charonIdx: charonIdx,
		started:   make(chan struct{}, 1),
	}
}

// Start starts a validator client.
func (v *CharonLHVCNode) Start(ctx context.Context) error {
	binaryPath, found := bazel.FindBinary("external/lighthouse", "lighthouse")
	if !found {
		log.Info(binaryPath)
		log.Error("lighthouse validator binary not found")
		return errors.New(" lighthouse validator binary not found")
	}

	err := v.importKey()
	if err != nil {
		return err
	}
	err = v.prepareTestnetDir()
	if err != nil {
		return err
	}

	charonIdx := v.charonIdx

	// Write stdout and stderr to log files.
	stdoutFile := path.Join(getCharonClusterPath(), "logs", fmt.Sprintf("lh_%d_stdout.log", charonIdx))
	stderrFile := path.Join(getCharonClusterPath(), "logs", fmt.Sprintf("lh_%d_stderr.log", charonIdx))
	// Write stdout and stderr to log files.
	stdout, err := os.Create(stdoutFile)
	if err != nil {
		return err
	}
	stderr, err := os.Create(stderrFile)
	if err != nil {
		return err
	}

	kPath := path.Join(getCharonClusterPath(), fmt.Sprintf("lh%d", charonIdx), "d")
	testNetDir := path.Join(getCharonClusterPath(), fmt.Sprintf("lh%d", charonIdx), "tn")
	args := []string{
		"validator_client",
		"--debug-level=debug",
		"--init-slashing-protection",
		fmt.Sprintf("--datadir=%s", kPath),
		fmt.Sprintf("--testnet-dir=%s", testNetDir),
		fmt.Sprintf("--beacon-nodes=http://localhost:%d", e2e.TestParams.Ports.CharonGatewayPort+charonIdx),
	}

	cmd := exec.CommandContext(ctx, binaryPath, args...) // #nosec G204 -- Safe

	defer func() {
		if err := stdout.Close(); err != nil {
			log.WithError(err).Error("Failed to close stdout file")
		}
		if err := stderr.Close(); err != nil {
			log.WithError(err).Error("Failed to close stderr file")
		}
	}()
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	log.Infof("Starting charon lighthouse validator client %d with flags: %s %s", charonIdx, binaryPath, strings.Join(args, " "))
	if err = cmd.Start(); err != nil {
		return err
	}

	// Mark node as ready.
	close(v.started)

	return cmd.Wait()
}

// Started checks whether validator node is started and ready to be queried.
func (v *CharonLHVCNode) Started() <-chan struct{} {
	return v.started
}

func (v *CharonLHVCNode) importKey() error {
	pubKeys := []string{
		"b12cd50aa60113f75987d6377ca6bad546573b0a8c2e4fa41d172676d055716a913328065dba9cb46af7547695c704bd",
		"b2c1c74095303d651b49ceecfd62f862ab41ac753780978532395bf36a1a5ce893c3fe845c50d90f1c3d60cf071b7dad",
		"917dc6d6102eb675372f837ee6309af6c5c1a55b372e7b2ef1f7831c783b03f306c91eba7a5dc0de7755ddd35c0e75a6",
		"93391da6bb2c972df35d5cc64219ebcd809df5a9d8fdd5f88793acae7401f8a94a812f7202f2f0b0941e345510f1cd59",
	}

	charonIdx := v.charonIdx

	testNetDir := path.Join(getCharonClusterPath(), fmt.Sprintf("lh%d", charonIdx), "tn")

	secretsPath := filepath.Join(testNetDir, "secrets")
	keystorePath := filepath.Join(testNetDir, "validators", "0x"+pubKeys[charonIdx])

	err := file.MkdirAll(secretsPath)
	if err != nil {
		return err
	}
	err = file.MkdirAll(keystorePath)
	if err != nil {
		return err
	}

	passwordSrcFilename := path.Join(getCharonClusterPath(), fmt.Sprintf("cluster/node%d/keystore-0.txt", charonIdx))
	keystoreSrcFilename := path.Join(getCharonClusterPath(), fmt.Sprintf("cluster/node%d/keystore-0.json", charonIdx))
	passwordFilename := filepath.Join(secretsPath, "0x"+pubKeys[charonIdx])
	keystoreFilename := filepath.Join(keystorePath, "voting-keystore.json")

	_, err = copyFile(passwordSrcFilename, passwordFilename)
	if err != nil {
		return err
	}
	_, err = copyFile(keystoreSrcFilename, keystoreFilename)
	if err != nil {
		return err
	}

	return nil
}

func (v *CharonLHVCNode) prepareTestnetDir() error {
	testNetDir := path.Join(getCharonClusterPath(), fmt.Sprintf("lh%d", v.charonIdx), "tn")

	err := file.WriteFile(filepath.Join(testNetDir, "deploy_block.txt"), []byte("0"))
	if err != nil {
		return err
	}

	configPath := filepath.Join(testNetDir, "config.yaml")
	rawYaml := params.E2EMainnetConfigYaml()
	// Add in deposit contract in yaml
	depContractStr := fmt.Sprintf("\nDEPOSIT_CONTRACT_ADDRESS: %#x", e2e.TestParams.ContractAddress)
	rawYaml = append(rawYaml, []byte(depContractStr)...)
	err = file.WriteFile(configPath, rawYaml)
	if err != nil {
		return err
	}

	return nil
}

func copyFile(src, dst string) (int64, error) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}

	nBytes, err := io.Copy(destination, source)

	err = source.Close()
	if err != nil {
		return nBytes, err
	}
	err = destination.Close()
	if err != nil {
		return nBytes, err
	}
	return nBytes, err
}
