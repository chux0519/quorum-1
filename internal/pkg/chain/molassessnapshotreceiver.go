package chain

import (
	p2pcrypto "github.com/libp2p/go-libp2p-core/crypto"
	localcrypto "github.com/rumsystem/quorum/internal/pkg/crypto"
	"github.com/rumsystem/quorum/internal/pkg/logging"
	"github.com/rumsystem/quorum/internal/pkg/nodectx"
	quorumpb "github.com/rumsystem/quorum/internal/pkg/pb"
	"google.golang.org/protobuf/proto"
)

var snapshotreceiver_log = logging.Logger("ssreceiver")

type MolassesSnapshotReceiver struct {
	grpItem           *quorumpb.GroupItem
	cIface            ChainMolassesIface
	nodename          string
	groupId           string
	snapshotpackages  map[string](map[string]*quorumpb.Snapshot)
	latestNonce       int64
	latestBlockId     string
	latestBlockHeight int64
	snapshotTag       *quorumpb.SnapShotTag
}

func (ssreceiver *MolassesSnapshotReceiver) Init(item *quorumpb.GroupItem, nodename string, iface ChainMolassesIface) {
	snapshotreceiver_log.Debugf("<%s> Init called", item.GroupId)
	ssreceiver.grpItem = item
	ssreceiver.cIface = iface
	ssreceiver.nodename = nodename
	ssreceiver.groupId = item.GroupId
	ssreceiver.snapshotpackages = make(map[string]map[string]*quorumpb.Snapshot)
	snapshotTag, err := nodectx.GetDbMgr().GetSnapshotTag(item.GroupId, nodename)
	if err != nil {
		snapshotTag = &quorumpb.SnapShotTag{}
		snapshotTag.Nonce = 0
		snapshotTag.HighestBlockId = ""
		snapshotTag.TimeStamp = 0
		snapshotTag.HighestHeight = 0
		snapshotTag.SenderPubkey = ""
		ssreceiver.snapshotTag = snapshotTag
	}
	ssreceiver.snapshotTag = snapshotTag
}

func (ssreceiver *MolassesSnapshotReceiver) ApplySnapshot(s *quorumpb.Snapshot) error {
	snapshotreceiver_log.Debugf("<%s> ApplySnapshot called", ssreceiver.groupId)

	//check if all snapshots are well received
	if _, ok := ssreceiver.snapshotpackages[s.SnapshotPackageId]; ok {
		//already receive snapshot with same SnapshotPackageId
		snapshotpackage, _ := ssreceiver.snapshotpackages[s.SnapshotPackageId]
		//check if snapshot is valid
		for _, received := range snapshotpackage {
			if received.TotalCount != s.TotalCount ||
				received.HighestBlockId != s.HighestBlockId ||
				received.HighestHeight != s.HighestHeight ||
				received.TimeStamp != s.TimeStamp ||
				received.Nonce != s.Nonce {
				//drop this snapshot and clear all snapshots with the same snapshotpackageId
				snapshotreceiver_log.Warnf("<%s> Invalid snapshot, snapshotId <%s>, snapshot package id <%s>, drop all snapshots with same snapshotId", ssreceiver.groupId, s.SnapshotId, s.SnapshotPackageId)
				delete(ssreceiver.snapshotpackages, s.SnapshotPackageId)
				return nil
			}
		}
		//add new snapshot to snapshot package
		snapshotpackage[s.SnapshotId] = s
	} else {
		//create new snapshot package
		var snapshotpackage map[string]*quorumpb.Snapshot
		snapshotpackage = make(map[string]*quorumpb.Snapshot)
		//add snapshot to package
		snapshotpackage[s.SnapshotId] = s
		//add new snapshot packages
		ssreceiver.snapshotpackages[s.SnapshotPackageId] = snapshotpackage
	}

	snapshotpackage, _ := ssreceiver.snapshotpackages[s.SnapshotPackageId]

	if len(snapshotpackage) == int(s.TotalCount) {
		snapshotreceiver_log.Debugf("<%s> apply snapshot", s.GroupId)
		if ssreceiver.snapshotTag.HighestBlockId == s.HighestBlockId &&
			ssreceiver.snapshotTag.HighestHeight == s.HighestHeight {
			snapshotreceiver_log.Debugf("<%s> snapshot already applied, only update snapshot tag", s.GroupId)
		} else {
			err := ssreceiver.applySnapshot(snapshotpackage)
			if err != nil {
				return err
			}
		}

		ssreceiver.snapshotTag.TimeStamp = s.TimeStamp
		ssreceiver.snapshotTag.HighestHeight = s.HighestHeight
		ssreceiver.snapshotTag.HighestBlockId = s.HighestBlockId
		ssreceiver.snapshotTag.Nonce = s.Nonce
		ssreceiver.snapshotTag.SnapshotPackageId = s.SnapshotPackageId
		ssreceiver.snapshotTag.SenderPubkey = s.SenderPubkey

		err := nodectx.GetDbMgr().UpdateSnapshotTag(ssreceiver.groupId, ssreceiver.snapshotTag, ssreceiver.nodename)
		if err != nil {
			return err
		}

		//remove snapshot package
		delete(ssreceiver.snapshotpackages, s.SnapshotPackageId)
	}

	return nil
}

func (ssreceiver *MolassesSnapshotReceiver) VerifySignature(s *quorumpb.Snapshot) (bool, error) {
	snapshotreceiver_log.Debugf("<%s> VerifySignature called", ssreceiver.groupId)
	var sig []byte
	sig = s.Singature
	s.Singature = nil
	bbytes, err := proto.Marshal(s)
	if err != nil {
		return false, err
	}
	hashed := localcrypto.Hash(bbytes)

	//create pubkey
	serializedpub, err := p2pcrypto.ConfigDecodeKey(s.SenderPubkey)
	if err != nil {
		return false, err
	}

	pubkey, err := p2pcrypto.UnmarshalPublicKey(serializedpub)
	if err != nil {
		return false, err
	}

	verify, err := pubkey.Verify(hashed, sig)
	s.Singature = sig
	return verify, err
}

func (ssreceiver *MolassesSnapshotReceiver) applySnapshot(snapshots map[string]*quorumpb.Snapshot) error {
	snapshotreceiver_log.Debugf("<%s> applySnapshot called", ssreceiver.groupId)
	for _, snapshot := range snapshots {
		for _, snapshotdata := range snapshot.SnapshotItems {
			if snapshotdata.Type == quorumpb.SnapShotItemType_SNAPSHOT_APP_CONFIG {
				err := nodectx.GetDbMgr().UpdSnapshotAppConfig(snapshotdata.Data)
				if err != nil {
					return err
				}
			} else if snapshotdata.Type == quorumpb.SnapShotItemType_SNAPSHOT_CHAIN_CONFIG {
				err := nodectx.GetDbMgr().UpdSnapshotChainConfig(snapshotdata.Data)
				if err != nil {
					return err
				}
			} else {
				snapshotreceiver_log.Warningf("<%s> Unknown snapshot data type", ssreceiver.groupId)
			}
		}
	}
	return nil
}
