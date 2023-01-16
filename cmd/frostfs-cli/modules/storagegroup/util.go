package storagegroup

import (
	"fmt"

	"github.com/TrueCloudLab/frostfs-node/cmd/frostfs-cli/internal/commonflags"
	commonCmd "github.com/TrueCloudLab/frostfs-node/cmd/internal/common"
	cid "github.com/TrueCloudLab/frostfs-sdk-go/container/id"
	oid "github.com/TrueCloudLab/frostfs-sdk-go/object/id"
	"github.com/spf13/cobra"
)

func readObjectAddress(cmd *cobra.Command, cnr *cid.ID, obj *oid.ID) oid.Address {
	readCID(cmd, cnr)
	readSGID(cmd, obj)

	var addr oid.Address
	addr.SetContainer(*cnr)
	addr.SetObject(*obj)
	return addr
}

func readCID(cmd *cobra.Command, id *cid.ID) {
	f := cmd.Flag(commonflags.CIDFlag)
	if f == nil {
		commonCmd.ExitOnErr(cmd, "", fmt.Errorf("missing container flag (%s)", commonflags.CIDFlag))
		return
	}

	err := id.DecodeString(f.Value.String())
	commonCmd.ExitOnErr(cmd, "decode container ID string: %w", err)
}

func readSGID(cmd *cobra.Command, id *oid.ID) {
	const flag = "id"

	f := cmd.Flag(flag)
	if f == nil {
		commonCmd.ExitOnErr(cmd, "", fmt.Errorf("missing storage group flag (%s)", flag))
		return
	}

	err := id.DecodeString(f.Value.String())
	commonCmd.ExitOnErr(cmd, "decode storage group ID string: %w", err)
}
