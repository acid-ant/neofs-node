package morph

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nspcc-dev/neo-go/pkg/io"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/policy"
	"github.com/nspcc-dev/neo-go/pkg/smartcontract/callflag"
	"github.com/nspcc-dev/neo-go/pkg/vm/emit"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	execFeeParam      = "ExecFeeFactor"
	storagePriceParam = "StoragePrice"
	setFeeParam       = "FeePerByte"
)

func setPolicyCmd(cmd *cobra.Command, args []string) error {
	wCtx, err := newInitializeContext(cmd, viper.GetViper())
	if err != nil {
		return fmt.Errorf("can't to initialize context: %w", err)
	}

	bw := io.NewBufBinWriter()
	for i := range args {
		k, v, found := strings.Cut(args[i], "=")
		if !found {
			return fmt.Errorf("invalid parameter format, must be Parameter=Value")
		}

		switch k {
		case execFeeParam, storagePriceParam, setFeeParam:
		default:
			return fmt.Errorf("parameter must be one of %s, %s and %s", execFeeParam, storagePriceParam, setFeeParam)
		}

		value, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return fmt.Errorf("can't parse parameter value '%s': %w", args[1], err)
		}

		emit.AppCall(bw.BinWriter, policy.Hash, "set"+k, callflag.All, int64(value))
	}

	if err := wCtx.sendCommitteeTx(bw.Bytes(), false); err != nil {
		return err
	}

	return wCtx.awaitTx()
}
