package components

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/pkg/errors"
	"github.com/prysmaticlabs/prysm/testing/endtoend/helpers"
	e2e "github.com/prysmaticlabs/prysm/testing/endtoend/params"
	e2etypes "github.com/prysmaticlabs/prysm/testing/endtoend/types"
)

type CharonNodeSet struct {
	e2etypes.ComponentRunner
	config  *e2etypes.E2EConfig
	started chan struct{}
}

// NewValidatorNodeSet creates and returns a set of validator nodes.
func NewCharonNodeSet(config *e2etypes.E2EConfig) *CharonNodeSet {
	return &CharonNodeSet{
		config:  config,
		started: make(chan struct{}, 1),
	}
}

// Start starts the configured amount of validators, also sending and mining their deposits.
func (s *CharonNodeSet) Start(ctx context.Context) error {
	// Always using genesis count since using anything else would be difficult to test for.
	charonNodeCount := e2e.TestParams.CharonNodeCount

	// Create validator nodes.
	nodes := make([]e2etypes.ComponentRunner, charonNodeCount)
	for i := 0; i < charonNodeCount; i++ {
		nodes[i] = NewCharonNode(s.config, i)
	}

	// Wait for all nodes to finish their job (blocking).
	// Once nodes are ready passed in handler function will be called.
	return helpers.WaitOnNodes(ctx, nodes, func() {
		// All nodes stated, close channel, so that all services waiting on a set, can proceed.
		close(s.started)
	})
}

// Started checks whether charon node set is started and all nodes are ready to be queried.
func (s *CharonNodeSet) Started() <-chan struct{} {
	return s.started
}

// CharonNode represents a charon node.
type CharonNode struct {
	e2etypes.ComponentRunner
	config       *e2etypes.E2EConfig
	started      chan struct{}
	validatorNum int
	index        int
	offset       int
}

// NewCharonNode creates and returns a charon node.
func NewCharonNode(config *e2etypes.E2EConfig, index int) *CharonNode {
	return &CharonNode{
		config:  config,
		index:   index,
		started: make(chan struct{}, 1),
	}
}

// Start starts a charon node.
func (v *CharonNode) Start(ctx context.Context) error {
	binaryPath, err := bazel.Runfile("third_party/charon/charon")
	if err != nil {
		return errors.Wrap(err, "charon binary not found")
	}

	index := v.index

	// TODO, this index is other index
	beaconRPCPort := e2e.TestParams.Ports.PrysmBeaconNodeRPCPort + index
	if beaconRPCPort >= e2e.TestParams.Ports.PrysmBeaconNodeRPCPort+e2e.TestParams.BeaconNodeCount {
		// Point any extra validator clients to a node we know is running.
		beaconRPCPort = e2e.TestParams.Ports.PrysmBeaconNodeRPCPort
	}

	args := []string{
		fmt.Sprintf("--%s=localhost:%d", "beacon-node-endoint", beaconRPCPort),
		fmt.Sprintf("--%s=localhost:%d", "validator-api-address", e2e.TestParams.Ports.CharonGatewayPort+index),
		fmt.Sprintf("--%s=localhost:%d", "p2p-tcp-address", e2e.TestParams.Ports.CharonTCPPort+index),
		fmt.Sprintf("--%s=localhost:%d", "p2p-udp-address", e2e.TestParams.Ports.CharonUDPPort+index),
		fmt.Sprintf("--%s=%s", "log-level", "debug"),
	}

	cmd := exec.CommandContext(ctx, binaryPath, args...)

	// Write stdout and stderr to log files.
	stdoutFile := path.Join(e2e.TestParams.LogPath, fmt.Sprintf("charon_%d_stdout.log", index))
	stdout, err := os.Create(stdoutFile)
	if err != nil {
		return err
	}
	stderrFile := path.Join(e2e.TestParams.LogPath, fmt.Sprintf("charon_%d_stderr.log", index))
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

	log.Infof("Starting charon node %d with flags: %s %s and output to %s %s", index, binaryPath, strings.Join(args, " "), stdoutFile, stderrFile)
	if err = cmd.Start(); err != nil {
		return err
	}

	// Mark node as ready.
	close(v.started)

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Error(err)
		}
	}()
	<-time.After(time.Minute)
	return nil
}

// Started checks whether charon node is started and ready to be queried.
func (v *CharonNode) Started() <-chan struct{} {
	return v.started
}
