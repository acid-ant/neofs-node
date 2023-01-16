package util

import (
	commonCmd "github.com/TrueCloudLab/frostfs-node/cmd/internal/common"
	locodedb "github.com/TrueCloudLab/frostfs-node/pkg/util/locode/db"
	locodebolt "github.com/TrueCloudLab/frostfs-node/pkg/util/locode/db/boltdb"
	"github.com/spf13/cobra"
)

const (
	locodeInfoDBFlag   = "db"
	locodeInfoCodeFlag = "locode"
)

var (
	locodeInfoDBPath string
	locodeInfoCode   string

	locodeInfoCmd = &cobra.Command{
		Use:   "info",
		Short: "Print information about UN/LOCODE from FrostFS database",
		Run: func(cmd *cobra.Command, _ []string) {
			targetDB := locodebolt.New(locodebolt.Prm{
				Path: locodeInfoDBPath,
			}, locodebolt.ReadOnly())

			err := targetDB.Open()
			commonCmd.ExitOnErr(cmd, "", err)

			defer targetDB.Close()

			record, err := locodedb.LocodeRecord(targetDB, locodeInfoCode)
			commonCmd.ExitOnErr(cmd, "", err)

			cmd.Printf("Country: %s\n", record.CountryName())
			cmd.Printf("Location: %s\n", record.LocationName())
			cmd.Printf("Continent: %s\n", record.Continent())
			if subDivCode := record.SubDivCode(); subDivCode != "" {
				cmd.Printf("Subdivision: [%s] %s\n", subDivCode, record.SubDivName())
			}

			geoPoint := record.GeoPoint()
			cmd.Printf("Coordinates: %0.2f, %0.2f\n", geoPoint.Latitude(), geoPoint.Longitude())
		},
	}
)

func initUtilLocodeInfoCmd() {
	flags := locodeInfoCmd.Flags()

	flags.StringVar(&locodeInfoDBPath, locodeInfoDBFlag, "", "Path to FrostFS UN/LOCODE database")
	_ = locodeInfoCmd.MarkFlagRequired(locodeInfoDBFlag)

	flags.StringVar(&locodeInfoCode, locodeInfoCodeFlag, "", "UN/LOCODE")
	_ = locodeInfoCmd.MarkFlagRequired(locodeInfoCodeFlag)
}
