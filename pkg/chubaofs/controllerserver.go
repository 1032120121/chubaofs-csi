/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package chubaofs

import (
	"fmt"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/volume/util"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

const (
	KMountPoint = "mountPoint"
	KVolumeName = "volName"
	KMasterAddr = "masterAddr"
	KLogDir     = "logDir"
	KWarnLogDir = "warnLogDir"
	KLogLevel   = "logLevel"
	KOwner      = "owner"
	KProfPort   = "profPort"
)

const (
	MinVolumeSize = util.GIB
)

const (
	KEY_VOLUME_NAME          = "volName"
	KEY_CFS_MASTER1          = "cfsMaster1"
	KEY_CFS_MASTER2          = "cfsMaster2"
	KEY_CFS_MASTER3          = "cfsMaster3"
	CFS_FUSE_CONFIG_PATH     = "/etc/cfs/fuse.json"
	FUSE_KEY_LOG_PATH_V1     = "logpath"
	FUSE_KEY_LOG_PATH_V2     = "logDir"
	FUSE_KEY_MASTER_ADDR_V1  = "master"
	FUSE_KEY_MASTER_ADDR_V2  = "masterAddr"
	FUSE_KEY_MOUNT_POINT_V1  = "mountpoint"
	FUSE_KEY_MOUNT_POINT_V2  = "mountPoint"
	FUSE_KEY_VOLUME_NAME_V1  = "volname"
	FUSE_KEY_VOLUME_NAME_V2  = "volName"
	FUSE_KEY_PROF_PORT_V1    = "profport"
	FUSE_KEY_PROF_PORT_V2    = "profPort"
	FUSE_KEY_LOG_LEVEL_V1    = "loglvl"
	FUSE_KEY_LOG_LEVEL_V2    = "logLevel"
	FUSE_KEY_LOOKUP_VALID_V1 = "lookupValid"
	FUSE_KEY_OWNER_V1        = "owner"
)

type controllerServer struct {
	caps []*csi.ControllerServiceCapability

	cfsMasterHostsLock sync.RWMutex
	cfsMasterHosts     map[string][]string
}

func NewControllerServer() *controllerServer {
	return &controllerServer{
		caps: getControllerServiceCapabilities(
			[]csi.ControllerServiceCapability_RPC_Type{
				csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			}),
	}
}

func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	glog.V(2).Infof("CreateVolume req:%v", req)
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.V(3).Infof("Invalid create volume req: %v", req)
		return nil, err
	}

	// Check arguments
	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}
	caps := req.GetVolumeCapabilities()
	if caps == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}

	// Keep a record of the requested access types.
	var accessTypeMount bool

	for _, cap := range caps {
		if cap.GetMount() != nil {
			accessTypeMount = true
		}
	}

	if !accessTypeMount {
		return nil, status.Error(codes.InvalidArgument, "Volume lack of mount access type")
	}

	capacity := int64(req.GetCapacityRange().GetRequiredBytes())
	if capacity < MinVolumeSize {
		capacity = MinVolumeSize
	}
	capacityInGIB, err := util.RoundUpSizeInt(capacity, util.GIB)
	if err != nil {
		glog.V(3).Infof("Invalid capacity size req: %v", req)
		return nil, err
	}

	volName := req.GetParameters()[KVolumeName]
	masterAddr := req.GetParameters()[KMasterAddr]
	master := strings.Split(masterAddr, ",")

	cs.putMasterHosts(volName, cfsMasterHost1, cfsMasterHost2, cfsMasterHost3)
	glog.V(4).Infof("GetName:%v", req.GetName())
	glog.V(4).Infof("GetParameters:%v", req.GetParameters())
	glog.V(4).Infof("allocate volume size(GB):%v for name:%v", cfsVolSizeGB, volName)

	cfsMasterLeader, err := GetClusterInfo(cfsMasterHost1)
	if err != nil {
		return nil, err
	}
	glog.V(4).Infof("CFS Master Leader Host is:%v", cfsMasterLeader)

	if err := CreateVolume(cfsMasterLeader, volName, capacityInGIB); err != nil {
		return nil, err
	}
	glog.V(2).Infof("CFS Create Volume:%v success.", volName)

	resp := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			Id:            volName,
			CapacityBytes: volSizeBytes,
			Attributes: map[string]string{
				KEY_VOLUME_NAME: volName,
				KEY_CFS_MASTER1: cfsMasterHost1,
				KEY_CFS_MASTER2: cfsMasterHost2,
				KEY_CFS_MASTER3: cfsMasterHost3,
			},
		},
	}
	return resp, nil
}

func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	glog.V(2).Infof("----------DeleteVolume req:%v", req)
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.Errorf("invalid delete volume req: %v", req)
		return nil, err
	}
	volumeId := req.VolumeId

	cfsMasterHosts := cs.getMasterHosts(volumeId)
	if len(cfsMasterHosts) == 0 {
		glog.Errorf("Not Found CFS master hosts for volumeId:%v", volumeId)
		return nil, fmt.Errorf("no master hosts")
	}

	GetClusterInfo(cfsMasterHosts[0])
	cfsMasterLeader, err := GetClusterInfo(cfsMasterHosts[0])
	if err != nil {
		return nil, err
	}
	glog.V(4).Infof("CFS Master Leader Host is:%v", cfsMasterLeader)

	if err := DeleteVolume(cfsMasterLeader, volumeId); err != nil {
		return nil, err
	}
	glog.V(2).Infof("Delete cfs volume :%s deleted success", volumeId)

	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	for _, cap := range req.VolumeCapabilities {
		if cap.GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
			return &csi.ValidateVolumeCapabilitiesResponse{Supported: false, Message: ""}, nil
		}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{Supported: true, Message: ""}, nil
}

func (cs *controllerServer) putMasterHosts(volumeName string, hosts ...string) {
	cs.cfsMasterHostsLock.Lock()
	defer cs.cfsMasterHostsLock.Unlock()
	cs.cfsMasterHosts[volumeName] = hosts
}

func (cs *controllerServer) getMasterHosts(volumeName string) []string {
	cs.cfsMasterHostsLock.Lock()
	defer cs.cfsMasterHostsLock.Unlock()
	hosts, found := cs.cfsMasterHosts[volumeName]
	if found {
		return hosts
	}
	return nil
}

func (cs *controllerServer) validateControllerServiceRequest(c csi.ControllerServiceCapability_RPC_Type) error {
	if c == csi.ControllerServiceCapability_RPC_UNKNOWN {
		return nil
	}

	for _, cap := range cs.caps {
		if c == cap.GetRpc().GetType() {
			return nil
		}
	}
	return status.Errorf(codes.InvalidArgument, "unsupported capability %s", c)
}

func getControllerServiceCapabilities(cl []csi.ControllerServiceCapability_RPC_Type) []*csi.ControllerServiceCapability {
	var csc []*csi.ControllerServiceCapability

	for _, cap := range cl {
		glog.Infof("Enabling controller service capability: %v", cap.String())
		csc = append(csc, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}

	return csc
}
