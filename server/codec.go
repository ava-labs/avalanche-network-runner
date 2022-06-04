package server

import (
	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/codec/linearcodec"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

const Version = 0

func createPChainCodec() (codec.Manager, error) {
	codecManager := codec.NewDefaultManager()
	lc := linearcodec.NewDefault()
	errs := wrappers.Errs{}
	errs.Add(
		lc.RegisterType(&platformvm.ProposalBlock{}),
		lc.RegisterType(&platformvm.AbortBlock{}),
		lc.RegisterType(&platformvm.CommitBlock{}),
		lc.RegisterType(&platformvm.StandardBlock{}),
		lc.RegisterType(&platformvm.AtomicBlock{}),
		lc.RegisterType(&secp256k1fx.TransferInput{}),
		lc.RegisterType(&secp256k1fx.MintOutput{}),
		lc.RegisterType(&secp256k1fx.TransferOutput{}),
		lc.RegisterType(&secp256k1fx.MintOperation{}),
		lc.RegisterType(&secp256k1fx.Credential{}),
		lc.RegisterType(&secp256k1fx.Input{}),
		lc.RegisterType(&secp256k1fx.OutputOwners{}),
		lc.RegisterType(&platformvm.UnsignedAddValidatorTx{}),
		lc.RegisterType(&platformvm.UnsignedAddSubnetValidatorTx{}),
		lc.RegisterType(&platformvm.UnsignedAddDelegatorTx{}),
		lc.RegisterType(&platformvm.UnsignedCreateChainTx{}),
		lc.RegisterType(&platformvm.UnsignedCreateSubnetTx{}),
		lc.RegisterType(&platformvm.UnsignedImportTx{}),
		lc.RegisterType(&platformvm.UnsignedExportTx{}),
		lc.RegisterType(&platformvm.UnsignedAdvanceTimeTx{}),
		lc.RegisterType(&platformvm.UnsignedRewardValidatorTx{}),
		lc.RegisterType(&stakeable.LockIn{}),
		lc.RegisterType(&stakeable.LockOut{}),
		codecManager.RegisterCodec(0, lc),
	)
	return codecManager, errs.Err
}
