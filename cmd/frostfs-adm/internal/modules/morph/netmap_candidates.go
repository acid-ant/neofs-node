package morph

import (
	"github.com/TrueCloudLab/frostfs-node/cmd/frostfs-adm/internal/commonflags"
	commonCmd "github.com/TrueCloudLab/frostfs-node/cmd/internal/common"
	"github.com/TrueCloudLab/frostfs-node/pkg/morph/client/netmap"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/invoker"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func listNetmapCandidatesNodes(cmd *cobra.Command, _ []string) {
	c, err := getN3Client(viper.GetViper())
	commonCmd.ExitOnErr(cmd, "can't create N3 client: %w", err)

	inv := invoker.New(c, nil)

	cs, err := c.GetContractStateByID(1)
	commonCmd.ExitOnErr(cmd, "can't get NNS contract info: %w", err)

	nmHash, err := nnsResolveHash(inv, cs.Hash, netmapContract+".frostfs")
	commonCmd.ExitOnErr(cmd, "can't get netmap contract hash: %w", err)

	res, err := inv.Call(nmHash, "netmapCandidates")
	commonCmd.ExitOnErr(cmd, "can't fetch list of network config keys from the netmap contract", err)
	nm, err := netmap.DecodeNetMap(res.Stack)
	commonCmd.ExitOnErr(cmd, "unable to decode netmap: %w", err)
	commonCmd.PrettyPrintNetMap(cmd, *nm, !viper.GetBool(commonflags.Verbose))
}
