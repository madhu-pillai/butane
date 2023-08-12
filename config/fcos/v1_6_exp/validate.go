// Copyright 2020 Red Hat, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.)

package v1_6_exp

import (
	//dutil "github.com/coreos/butane/config/util"
	"github.com/coreos/butane/config/common"
	"github.com/coreos/ignition/v2/config/util"

	"github.com/coreos/vcontext/path"
	"github.com/coreos/vcontext/report"
)

func (d BootDevice) Validate(c path.ContextPath) (r report.Report) {
	if d.Layout != nil {
		switch *d.Layout {
		case "aarch64", "ppc64le", "x86_64", "s390x-eckd", "s390x-virt", "s390x-zfcp":
		default:
			r.AddOnError(c.Append("layout"), common.ErrUnknownBootDeviceLayout)
		}
	}
	// Validate layout s390x specific device-s390x not for other arch.
	// s390x layout does not support mirror
	if d.Layout != nil {
		switch {
		case *d.Layout == "s390x-eckd" && util.NilOrEmpty(d.Luks.Device):
			r.AddOnError(c.Append("device-s390x"), common.ErrNoLuksBootDevice)
		case *d.Layout == "s390x-zfcp" && util.NilOrEmpty(d.Luks.Device):
			r.AddOnError(c.Append("device-s390x"), common.ErrNoLuksBootDevice) 
		case *d.Layout == "s390x-eckd" && len(d.Mirror.Devices) > 0:
			r.AddOnError(c.Append("device-s390x"), common.ErrMirrorNotSupport)	
		case *d.Layout == "s390x-zfcp" && len(d.Mirror.Devices) > 0:
			r.AddOnError(c.Append("device-s390x"), common.ErrMirrorNotSupport)	
		}
	}
	// //Validate the devices passed to device-s390x are arch specific.
	// if d.Layout != nil {
	// 	disk_s390x := dutil.DiskVal(*d.Luks.Device)
	// 	switch {
	// 	case *d.Layout == "s390x-eckd" && disk_s390x != "dasd":
	// 		r.AddOnError(c.Append("device-s390x"), common.ErrNoLuksBootDevice)	
	// 	case *d.Layout == "s390x-zfcp" && disk_s390x != "sd":
	// 		r.AddOnError(c.Append("device-s390x"), common.ErrNoLuksBootDevice)
	// 	}
	// }
	r.Merge(d.Mirror.Validate(c.Append("mirror")))
	return
}

func (m BootDeviceMirror) Validate(c path.ContextPath) (r report.Report) {
	if len(m.Devices) == 1 {
		r.AddOnError(c.Append("devices"), common.ErrTooFewMirrorDevices)
	}
	return
}

func (user GrubUser) Validate(c path.ContextPath) (r report.Report) {
	if user.Name == "" {
		r.AddOnError(c.Append("name"), common.ErrGrubUserNameNotSpecified)
	}

	if !util.NotEmpty(user.PasswordHash) {
		r.AddOnError(c.Append("password_hash"), common.ErrGrubPasswordNotSpecified)
	}
	return
}
