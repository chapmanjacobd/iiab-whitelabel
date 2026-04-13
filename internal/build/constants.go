package build

import (
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
)

const (
	DebianTarURL = "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.tar.xz"
	// Ubuntu cloud image URL for ubuntu26.04 (noble daily build)
	UbuntuTarURL = "https://cdimage.ubuntu.com/ubuntu-server/daily-live/current/noble-server-cloudimg-amd64.img"
	IIABRepo     = "https://github.com/iiab/iiab.git"

	// expectTimeout is the default timeout for IIAB install (2 hours)
	expectTimeout = 7200 * time.Second

	// BridgeName is the bridge name for IIAB demos.
	BridgeName = network.BridgeName
	// Gateway is the gateway IP for IIAB demos.
	Gateway = network.Gateway
)
