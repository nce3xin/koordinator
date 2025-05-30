/*
Copyright 2022 The Koordinator Authors.

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

package resourceexecutor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const ErrResctrlDir = "resctrl path or file not exist"
const CacheIdIndex = 2

// NewResctrlReader: lazy resctrl reader, just check vendor to generate specific reader
func NewResctrlReader() ResctrlReader {
	// Support two main platforms; other platforms need to add their implementation of the resctrl interface.
	if vendorId, err := system.GetVendorIDByCPUInfo(system.GetCPUInfoPath()); err != nil {
		klog.V(0).ErrorS(err, "get cpu vendor error, stop start resctrl collector")
		return &fakeReader{}
	} else {
		switch vendorId {
		case system.INTEL_VENDOR_ID:
			return NewResctrlRDTReader()
		case system.AMD_VENDOR_ID:
			return NewResctrlQoSReader()
		default:
			klog.V(0).ErrorS(err, "unsupported cpu vendor")
		}
	}
	return &fakeReader{}
}

type CacheId int

// parent for resctrl is like: `BE`, `LS`
type ResctrlReader interface {
	ReadResctrlL3Stat(parent string) (map[CacheId]uint64, error)
	ReadResctrlMBStat(parent string) (map[CacheId]system.MBStatData, error)
}

type ResctrlBaseReader struct {
}

type ResctrlRDTReader struct {
	ResctrlBaseReader
}
type ResctrlAMDReader struct {
	ResctrlBaseReader
}

type fakeReader struct {
	ResctrlBaseReader
}

func (rr *fakeReader) ReadResctrlL3Stat(parent string) (map[CacheId]uint64, error) {
	return nil, errors.New("unsupported platform")
}

func (rr *fakeReader) ReadResctrlMBStat(parent string) (map[CacheId]system.MBStatData, error) {
	return nil, errors.New("unsupported platform")
}

func NewResctrlRDTReader() ResctrlReader {
	return &ResctrlRDTReader{}
}

func NewResctrlQoSReader() ResctrlReader {
	return &ResctrlAMDReader{}
}

// ReadResctrlL3Stat: Reads the resctrl L3 cache statistics based on NUMA domain.
// For more information about x86 resctrl, refer to: https://docs.kernel.org/arch/x86/resctrl.html
func (rr *ResctrlBaseReader) ReadResctrlL3Stat(parent string) (map[CacheId]uint64, error) {
	l3Stat := make(map[CacheId]uint64)
	monDataPath := system.GetResctrlMonDataPath(parent)
	fd, err := os.Open(monDataPath)
	if err != nil {
		return nil, errors.New(ErrResctrlDir)
	}
	defer fd.Close()
	// read all l3-memory domains
	domains, err := fd.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("%s, cannot find L3 domains, err: %w", ErrResctrlDir, err)
	}
	for _, domain := range domains {
		// Convert the cache ID from the domain name string to an integer.
		cacheId, err := strconv.Atoi(strings.Split(domain.Name(), "_")[CacheIdIndex])
		if err != nil {
			return nil, fmt.Errorf("%s, cannot get cacheid, err: %w", ErrResctrlDir, err)
		}
		// Construct the path to the resctrl L3 cache occupancy file.
		path := system.ResctrlLLCOccupancy.Path(filepath.Join(parent, system.ResctrlMonData, domain.Name()))
		l3Byte, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s, cannot read from resctrl file system, err: %w",
				ErrResctrlDir, err)
		}
		// Parse the L3 cache usage data from the file content.
		l3Usage, err := strconv.ParseUint(string(l3Byte), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot parse L3 cache usage, err: %w", err)
		}
		l3Stat[CacheId(cacheId)] = l3Usage
	}
	return l3Stat, nil
}

// ReadResctrlMBStat: Reads the resctrl memory bandwidth statistics based on NUMA domain.
// For more information about x86 resctrl, refer to: https://docs.kernel.org/arch/x86/resctrl.html
func (rr *ResctrlBaseReader) ReadResctrlMBStat(parent string) (map[CacheId]system.MBStatData, error) {
	mbStat := make(map[CacheId]system.MBStatData)
	monDataPath := system.GetResctrlMonDataPath(parent)
	fd, err := os.Open(monDataPath)
	if err != nil {
		return nil, errors.New(ErrResctrlDir)
	}
	// read all l3-memory domains
	domains, err := fd.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("%s, cannot find L3 domains, err: %w", ErrResctrlDir, err)
	}
	for _, domain := range domains {
		// Parse the L3 cache usage data from the file content.
		cacheId, err := strconv.Atoi(strings.Split(domain.Name(), "_")[CacheIdIndex])
		if err != nil {
			return nil, fmt.Errorf("%s, cannot get cacheid, err: %w", ErrResctrlDir, err)
		}
		mbStat[CacheId(cacheId)] = make(system.MBStatData)
		// Read the memory bandwidth statistics for the local and total memory bandwidth.
		// The local memory bandwidth is the memory bandwidth consumed by the domain itself.
		// The total memory bandwidth is the memory bandwidth consumed by the domain and accessed by other domains.
		for _, mbResource := range []system.Resource{
			system.ResctrlMBLocal, system.ResctrlMBTotal,
		} {
			contentName := mbResource.Path(filepath.Join(parent, system.ResctrlMonData, domain.Name()))
			contentByte, err := os.ReadFile(contentName)
			if err != nil {
				return nil, fmt.Errorf("%s, cannot read from resctrl file system, err: %w",
					ErrResctrlDir, err)
			}
			mbUsage, err := strconv.ParseUint(string(contentByte), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse result, err: %w", err)
			}
			mbStat[CacheId(cacheId)][string(mbResource.ResourceType())] = mbUsage
		}
	}
	return mbStat, nil
}
