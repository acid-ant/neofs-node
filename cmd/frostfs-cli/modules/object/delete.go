package object

import (
	"fmt"

	internalclient "github.com/TrueCloudLab/frostfs-node/cmd/frostfs-cli/internal/client"
	"github.com/TrueCloudLab/frostfs-node/cmd/frostfs-cli/internal/commonflags"
	"github.com/TrueCloudLab/frostfs-node/cmd/frostfs-cli/internal/key"
	commonCmd "github.com/TrueCloudLab/frostfs-node/cmd/internal/common"
	cid "github.com/TrueCloudLab/frostfs-sdk-go/container/id"
	oid "github.com/TrueCloudLab/frostfs-sdk-go/object/id"
	"github.com/spf13/cobra"
)

var objectDelCmd = &cobra.Command{
	Use:     "delete",
	Aliases: []string{"del"},
	Short:   "Delete object from FrostFS",
	Long:    "Delete object from FrostFS",
	Run:     deleteObject,
}

func initObjectDeleteCmd() {
	commonflags.Init(objectDelCmd)
	initFlagSession(objectDelCmd, "DELETE")

	flags := objectDelCmd.Flags()

	flags.String(commonflags.CIDFlag, "", commonflags.CIDFlagUsage)
	flags.String(commonflags.OIDFlag, "", commonflags.OIDFlagUsage)
	flags.Bool(binaryFlag, false, "Deserialize object structure from given file.")
	flags.String(fileFlag, "", "File with object payload")
}

func deleteObject(cmd *cobra.Command, _ []string) {
	var cnr cid.ID
	var obj oid.ID
	var objAddr oid.Address

	binary, _ := cmd.Flags().GetBool(binaryFlag)
	if binary {
		filename, _ := cmd.Flags().GetString(fileFlag)
		if filename == "" {
			commonCmd.ExitOnErr(cmd, "", fmt.Errorf("required flag \"%s\" not set", fileFlag))
		}
		objAddr = readObjectAddressBin(cmd, &cnr, &obj, filename)
	} else {
		cidVal, _ := cmd.Flags().GetString(commonflags.CIDFlag)
		if cidVal == "" {
			commonCmd.ExitOnErr(cmd, "", fmt.Errorf("required flag \"%s\" not set", commonflags.CIDFlag))
		}

		oidVal, _ := cmd.Flags().GetString(commonflags.OIDFlag)
		if oidVal == "" {
			commonCmd.ExitOnErr(cmd, "", fmt.Errorf("required flag \"%s\" not set", commonflags.OIDFlag))
		}

		objAddr = readObjectAddress(cmd, &cnr, &obj)
	}

	pk := key.GetOrGenerate(cmd)

	var prm internalclient.DeleteObjectPrm
	ReadOrOpenSession(cmd, &prm, pk, cnr, &obj)
	Prepare(cmd, &prm)
	prm.SetAddress(objAddr)

	res, err := internalclient.DeleteObject(prm)
	commonCmd.ExitOnErr(cmd, "rpc error: %w", err)

	tomb := res.Tombstone()

	cmd.Println("Object removed successfully.")
	cmd.Printf("  ID: %s\n  CID: %s\n", tomb, cnr)
}
