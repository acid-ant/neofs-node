package client

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/nspcc-dev/neo-go/pkg/core/native/nativenames"
	"github.com/nspcc-dev/neo-go/pkg/core/native/noderoles"
	"github.com/nspcc-dev/neo-go/pkg/core/transaction"
	"github.com/nspcc-dev/neo-go/pkg/crypto/keys"
	"github.com/nspcc-dev/neo-go/pkg/encoding/fixedn"
	"github.com/nspcc-dev/neo-go/pkg/rpc/client"
	sc "github.com/nspcc-dev/neo-go/pkg/smartcontract"
	"github.com/nspcc-dev/neo-go/pkg/smartcontract/trigger"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"github.com/nspcc-dev/neo-go/pkg/vm"
	"github.com/nspcc-dev/neo-go/pkg/vm/stackitem"
	"github.com/nspcc-dev/neo-go/pkg/wallet"
	"github.com/nspcc-dev/neofs-node/pkg/util/logger"
	"go.uber.org/zap"
)

// Client is a wrapper over web socket neo-go client
// that provides smart-contract invocation interface
// and notification subscription functionality.
//
// On connection lost tries establishing new connection
// to the next RPC (if any). If no RPC node available,
// switches to inactive mode: any RPC call leads to immediate
// return with ErrConnectionLost error, notification channel
// returned from Client.NotificationChannel is closed.
//
// Working client must be created via constructor New.
// Using the Client that has been created with new(Client)
// expression (or just declaring a Client variable) is unsafe
// and can lead to panic.
type Client struct {
	cache cache

	logger *logger.Logger // logging component

	client *client.WSClient // neo-go websocket client

	acc *wallet.Account // neo account

	signer *transaction.Signer

	notary *notary

	cfg cfg

	endpoints *endpoints

	// switching between rpc endpoint lock
	switchLock *sync.RWMutex

	// channel for ws notifications
	notifications chan client.Notification

	// channel for internal stop
	closeChan chan struct{}

	// cached subscription information
	subscribedEvents       map[util.Uint160]string
	subscribedNotaryEvents map[util.Uint160]string
	subscribedToNewBlocks  bool

	// indicates that Client is not able to
	// establish connection to any of the
	// provided RPC endpoints
	inactive bool
}

type cache struct {
	m *sync.RWMutex

	nnsHash   *util.Uint160
	gKey      *keys.PublicKey
	txHeights *lru.Cache
}

func (c cache) nns() *util.Uint160 {
	c.m.RLock()
	defer c.m.RUnlock()

	return c.nnsHash
}

func (c *cache) setNNSHash(nnsHash util.Uint160) {
	c.m.Lock()
	defer c.m.Unlock()

	c.nnsHash = &nnsHash
}

func (c cache) groupKey() *keys.PublicKey {
	c.m.RLock()
	defer c.m.RUnlock()

	return c.gKey
}

func (c *cache) setGroupKey(groupKey *keys.PublicKey) {
	c.m.Lock()
	defer c.m.Unlock()

	c.gKey = groupKey
}

func (c *cache) invalidate() {
	c.m.Lock()
	defer c.m.Unlock()

	c.nnsHash = nil
	c.gKey = nil
	c.txHeights.Purge()
}

var (
	// ErrNilClient is returned by functions that expect
	// a non-nil Client pointer, but received nil.
	ErrNilClient = errors.New("client is nil")

	// ErrConnectionLost is returned when client lost web socket connection
	// to the RPC node and has not been able to establish a new one since.
	ErrConnectionLost = errors.New("connection to the RPC node has been lost")
)

// HaltState returned if TestInvoke function processed without panic.
const HaltState = "HALT"

type notHaltStateError struct {
	state, exception string
}

func (e *notHaltStateError) Error() string {
	return fmt.Sprintf(
		"chain/client: contract execution finished with state %s; exception: %s",
		e.state,
		e.exception,
	)
}

var errEmptyInvocationScript = errors.New("got empty invocation script from neo node")

// implementation of error interface for NeoFS-specific errors.
type neofsError struct {
	err error
}

func (e neofsError) Error() string {
	return fmt.Sprintf("neofs error: %v", e.err)
}

// wraps NeoFS-specific error into neofsError. Arg must not be nil.
func wrapNeoFSError(err error) error {
	return neofsError{err}
}

// Invoke invokes contract method by sending transaction into blockchain.
// Supported args types: int64, string, util.Uint160, []byte and bool.
func (c *Client) Invoke(contract util.Uint160, fee fixedn.Fixed8, method string, args ...interface{}) error {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return ErrConnectionLost
	}

	params := make([]sc.Parameter, 0, len(args))

	for i := range args {
		param, err := toStackParameter(args[i])
		if err != nil {
			return err
		}

		params = append(params, param)
	}

	cosigner := []transaction.Signer{
		{
			Account:          c.acc.PrivateKey().PublicKey().GetScriptHash(),
			Scopes:           c.signer.Scopes,
			AllowedContracts: c.signer.AllowedContracts,
			AllowedGroups:    c.signer.AllowedGroups,
		},
	}

	cosignerAcc := []client.SignerAccount{
		{
			Signer:  cosigner[0],
			Account: c.acc,
		},
	}

	resp, err := c.client.InvokeFunction(contract, method, params, cosigner)
	if err != nil {
		return err
	}

	if resp.State != HaltState {
		return wrapNeoFSError(&notHaltStateError{state: resp.State, exception: resp.FaultException})
	}

	if len(resp.Script) == 0 {
		return wrapNeoFSError(errEmptyInvocationScript)
	}

	script := resp.Script

	sysFee := resp.GasConsumed + int64(fee) // consumed gas + extra fee

	txHash, err := c.client.SignAndPushInvocationTx(script, c.acc, sysFee, 0, cosignerAcc)
	if err != nil {
		return err
	}

	c.logger.Debug("neo client invoke",
		zap.String("method", method),
		zap.Stringer("tx_hash", txHash.Reverse()))

	return nil
}

// TestInvoke invokes contract method locally in neo-go node. This method should
// be used to read data from smart-contract.
func (c *Client) TestInvoke(contract util.Uint160, method string, args ...interface{}) (res []stackitem.Item, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return nil, ErrConnectionLost
	}

	var params = make([]sc.Parameter, 0, len(args))

	for i := range args {
		p, err := toStackParameter(args[i])
		if err != nil {
			return nil, err
		}

		params = append(params, p)
	}

	cosigner := []transaction.Signer{
		{
			Account: c.acc.PrivateKey().PublicKey().GetScriptHash(),
			Scopes:  transaction.Global,
		},
	}

	val, err := c.client.InvokeFunction(contract, method, params, cosigner)
	if err != nil {
		return nil, err
	}

	if val.State != HaltState {
		return nil, wrapNeoFSError(&notHaltStateError{state: val.State, exception: val.FaultException})
	}

	return val.Stack, nil
}

// TransferGas to the receiver from local wallet
func (c *Client) TransferGas(receiver util.Uint160, amount fixedn.Fixed8) error {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return ErrConnectionLost
	}

	gas, err := c.client.GetNativeContractHash(nativenames.Gas)
	if err != nil {
		return err
	}

	txHash, err := c.client.TransferNEP17(c.acc, receiver, gas, int64(amount), 0, nil, nil)
	if err != nil {
		return err
	}

	c.logger.Debug("native gas transfer invoke",
		zap.String("to", receiver.StringLE()),
		zap.Stringer("tx_hash", txHash.Reverse()))

	return nil
}

// Wait function blocks routing execution until there
// are `n` new blocks in the chain.
//
// Returns only connection errors.
func (c *Client) Wait(ctx context.Context, n uint32) error {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return ErrConnectionLost
	}

	var (
		err               error
		height, newHeight uint32
	)

	height, err = c.client.GetBlockCount()
	if err != nil {
		c.logger.Error("can't get blockchain height",
			zap.String("error", err.Error()))
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		newHeight, err = c.client.GetBlockCount()
		if err != nil {
			c.logger.Error("can't get blockchain height",
				zap.String("error", err.Error()))
			return nil
		}

		if newHeight >= height+n {
			return nil
		}

		time.Sleep(c.cfg.waitInterval)
	}
}

// GasBalance returns GAS amount in the client's wallet.
func (c *Client) GasBalance() (res int64, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return 0, ErrConnectionLost
	}

	gas, err := c.client.GetNativeContractHash(nativenames.Gas)
	if err != nil {
		return 0, err
	}

	return c.client.NEP17BalanceOf(gas, c.acc.PrivateKey().GetScriptHash())
}

// Committee returns keys of chain committee from neo native contract.
func (c *Client) Committee() (res keys.PublicKeys, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return nil, ErrConnectionLost
	}

	return c.client.GetCommittee()
}

// TxHalt returns true if transaction has been successfully executed and persisted.
func (c *Client) TxHalt(h util.Uint256) (res bool, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return false, ErrConnectionLost
	}

	trig := trigger.Application
	aer, err := c.client.GetApplicationLog(h, &trig)
	if err != nil {
		return false, err
	}
	return len(aer.Executions) > 0 && aer.Executions[0].VMState.HasFlag(vm.HaltState), nil
}

// TxHeight returns true if transaction has been successfully executed and persisted.
func (c *Client) TxHeight(h util.Uint256) (res uint32, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return 0, ErrConnectionLost
	}

	return c.client.GetTransactionHeight(h)
}

// NeoFSAlphabetList returns keys that stored in NeoFS Alphabet role. Main chain
// stores alphabet node keys of inner ring there, however the sidechain stores both
// alphabet and non alphabet node keys of inner ring.
func (c *Client) NeoFSAlphabetList() (res keys.PublicKeys, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return nil, ErrConnectionLost
	}

	list, err := c.roleList(noderoles.NeoFSAlphabet)
	if err != nil {
		return nil, fmt.Errorf("can't get alphabet nodes role list: %w", err)
	}

	return list, nil
}

// GetDesignateHash returns hash of the native `RoleManagement` contract.
func (c *Client) GetDesignateHash() (res util.Uint160, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return util.Uint160{}, ErrConnectionLost
	}

	return c.client.GetNativeContractHash(nativenames.Designation)
}

func (c *Client) roleList(r noderoles.Role) (keys.PublicKeys, error) {
	height, err := c.client.GetBlockCount()
	if err != nil {
		return nil, fmt.Errorf("can't get chain height: %w", err)
	}

	return c.client.GetDesignatedByRole(r, height)
}

// tries to resolve sc.Parameter from the arg.
//
// Wraps any error to neofsError.
func toStackParameter(value interface{}) (sc.Parameter, error) {
	var result = sc.Parameter{
		Value: value,
	}

	switch v := value.(type) {
	case []byte:
		result.Type = sc.ByteArrayType
	case int:
		result.Type = sc.IntegerType
		result.Value = big.NewInt(int64(v))
	case int64:
		result.Type = sc.IntegerType
		result.Value = big.NewInt(v)
	case uint64:
		result.Type = sc.IntegerType
		result.Value = new(big.Int).SetUint64(v)
	case [][]byte:
		arr := make([]sc.Parameter, 0, len(v))
		for i := range v {
			elem, err := toStackParameter(v[i])
			if err != nil {
				return result, err
			}

			arr = append(arr, elem)
		}

		result.Type = sc.ArrayType
		result.Value = arr
	case string:
		result.Type = sc.StringType
	case util.Uint160:
		result.Type = sc.ByteArrayType
		result.Value = v.BytesBE()
	case noderoles.Role:
		result.Type = sc.IntegerType
		result.Value = big.NewInt(int64(v))
	case keys.PublicKeys:
		arr := make([][]byte, 0, len(v))
		for i := range v {
			arr = append(arr, v[i].Bytes())
		}

		return toStackParameter(arr)
	case bool:
		result.Type = sc.BoolType
		result.Value = v
	default:
		return result, wrapNeoFSError(fmt.Errorf("chain/client: unsupported parameter %v", value))
	}

	return result, nil
}

// MagicNumber returns the magic number of the network
// to which the underlying RPC node client is connected.
func (c *Client) MagicNumber() (uint64, error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return 0, ErrConnectionLost
	}

	mNum, err := c.client.GetNetwork()
	if err != nil {
		// error appears only if client
		// has not been initialized
		panic(err)
	}

	return uint64(mNum), nil
}

// BlockCount returns block count of the network
// to which the underlying RPC node client is connected.
func (c *Client) BlockCount() (res uint32, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return 0, ErrConnectionLost
	}

	return c.client.GetBlockCount()
}

// MsPerBlock returns MillisecondsPerBlock network parameter.
func (c *Client) MsPerBlock() (res int64, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return 0, ErrConnectionLost
	}

	v, err := c.client.GetVersion()
	if err != nil {
		return 0, fmt.Errorf("getVersion: %w", err)
	}

	return int64(v.Protocol.MillisecondsPerBlock), nil
}

// IsValidScript returns true if invocation script executes with HALT state.
func (c *Client) IsValidScript(script []byte, signers []transaction.Signer) (res bool, err error) {
	c.switchLock.RLock()
	defer c.switchLock.RUnlock()

	if c.inactive {
		return false, ErrConnectionLost
	}

	result, err := c.client.InvokeScript(script, signers)
	if err != nil {
		return false, fmt.Errorf("invokeScript: %w", err)
	}

	return result.State == vm.HaltState.String(), nil
}

// NotificationChannel returns channel than receives subscribed
// notification from the connected RPC node.
// Channel is closed when connection to the RPC node has been
// lost without the possibility of recovery.
func (c *Client) NotificationChannel() <-chan client.Notification {
	return c.notifications
}

// inactiveMode switches Client to an inactive mode:
// - notification channel is closed;
// - all the new RPC request would return ErrConnectionLost;
// - inactiveModeCb is called if not nil.
func (c *Client) inactiveMode() {
	c.switchLock.Lock()
	defer c.switchLock.Unlock()

	close(c.notifications)
	c.inactive = true

	if c.cfg.inactiveModeCb != nil {
		c.cfg.inactiveModeCb()
	}
}
