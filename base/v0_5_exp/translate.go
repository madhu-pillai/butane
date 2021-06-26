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

package v0_5_exp

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	baseutil "github.com/coreos/butane/base/util"
	"github.com/coreos/butane/config/common"
	"github.com/coreos/butane/translate"

	"github.com/coreos/go-systemd/unit"
	"github.com/coreos/ignition/v2/config/util"
	"github.com/coreos/ignition/v2/config/v3_3/types"
	"github.com/coreos/vcontext/path"
	"github.com/coreos/vcontext/report"
)

var (
	mountUnitTemplate = template.Must(template.New("unit").Parse(`
{{- define "options" }}
  {{- if .MountOptions }}
Options=
    {{- range $i, $opt := .MountOptions }}
      {{- if $i }},{{ end }}
      {{- $opt }}
    {{- end }}
  {{- end }}
{{- end -}}

# Generated by Butane
{{- if .Swap }}
[Swap]
What={{.Device}}
{{- template "options" . }}

[Install]
RequiredBy=swap.target
{{- else }}
[Unit]
{{- if .Remote }}
Before=remote-fs.target
DefaultDependencies=no
{{- else }}
Before=local-fs.target
{{- end }}
Requires=systemd-fsck@{{.EscapedDevice}}.service
After=systemd-fsck@{{.EscapedDevice}}.service

[Mount]
Where={{.Path}}
What={{.Device}}
Type={{.Format}}
{{- template "options" . }}

[Install]
{{- if .Remote }}
RequiredBy=remote-fs.target
{{- else }}
RequiredBy=local-fs.target
{{- end }}
{{- end }}`))
)

// ToIgn3_3Unvalidated translates the config to an Ignition config. It also returns the set of translations
// it did so paths in the resultant config can be tracked back to their source in the source config.
// No config validation is performed on input or output.
func (c Config) ToIgn3_3Unvalidated(options common.TranslateOptions) (types.Config, translate.TranslationSet, report.Report) {
	ret := types.Config{}

	tr := translate.NewTranslator("yaml", "json", options)
	tr.AddCustomTranslator(translateIgnition)
	tr.AddCustomTranslator(translateFile)
	tr.AddCustomTranslator(translateDirectory)
	tr.AddCustomTranslator(translateLink)
	tr.AddCustomTranslator(translateResource)

	tm, r := translate.Prefixed(tr, "ignition", &c.Ignition, &ret.Ignition)
	tm.AddTranslation(path.New("yaml", "version"), path.New("json", "ignition", "version"))
	tm.AddTranslation(path.New("yaml", "ignition"), path.New("json", "ignition"))
	translate.MergeP(tr, tm, &r, "kernel_arguments", &c.KernelArguments, &ret.KernelArguments)
	translate.MergeP(tr, tm, &r, "passwd", &c.Passwd, &ret.Passwd)
	translate.MergeP(tr, tm, &r, "storage", &c.Storage, &ret.Storage)
	translate.MergeP(tr, tm, &r, "systemd", &c.Systemd, &ret.Systemd)

	c.addMountUnits(&ret, &tm)

	tm2, r2 := c.processTrees(&ret, options)
	tm.Merge(tm2)
	r.Merge(r2)

	if r.IsFatal() {
		return types.Config{}, translate.TranslationSet{}, r
	}
	return ret, tm, r
}

func translateIgnition(from Ignition, options common.TranslateOptions) (to types.Ignition, tm translate.TranslationSet, r report.Report) {
	tr := translate.NewTranslator("yaml", "json", options)
	tr.AddCustomTranslator(translateResource)
	to.Version = types.MaxVersion.String()
	tm, r = translate.Prefixed(tr, "config", &from.Config, &to.Config)
	translate.MergeP(tr, tm, &r, "proxy", &from.Proxy, &to.Proxy)
	translate.MergeP(tr, tm, &r, "security", &from.Security, &to.Security)
	translate.MergeP(tr, tm, &r, "timeouts", &from.Timeouts, &to.Timeouts)
	return
}

func translateFile(from File, options common.TranslateOptions) (to types.File, tm translate.TranslationSet, r report.Report) {
	tr := translate.NewTranslator("yaml", "json", options)
	tr.AddCustomTranslator(translateResource)
	tm, r = translate.Prefixed(tr, "group", &from.Group, &to.Group)
	translate.MergeP(tr, tm, &r, "user", &from.User, &to.User)
	translate.MergeP(tr, tm, &r, "append", &from.Append, &to.Append)
	translate.MergeP(tr, tm, &r, "contents", &from.Contents, &to.Contents)
	translate.MergeP(tr, tm, &r, "overwrite", &from.Overwrite, &to.Overwrite)
	translate.MergeP(tr, tm, &r, "path", &from.Path, &to.Path)
	translate.MergeP(tr, tm, &r, "mode", &from.Mode, &to.Mode)
	return
}

func translateResource(from Resource, options common.TranslateOptions) (to types.Resource, tm translate.TranslationSet, r report.Report) {
	tr := translate.NewTranslator("yaml", "json", options)
	tm, r = translate.Prefixed(tr, "verification", &from.Verification, &to.Verification)
	translate.MergeP(tr, tm, &r, "httpHeaders", &from.HTTPHeaders, &to.HTTPHeaders)
	translate.MergeP(tr, tm, &r, "source", &from.Source, &to.Source)
	translate.MergeP(tr, tm, &r, "compression", &from.Compression, &to.Compression)

	if from.Local != nil {
		c := path.New("yaml", "local")

		if options.FilesDir == "" {
			r.AddOnError(c, common.ErrNoFilesDir)
			return
		}

		// calculate file path within FilesDir and check for
		// path traversal
		filePath := filepath.Join(options.FilesDir, *from.Local)
		if err := baseutil.EnsurePathWithinFilesDir(filePath, options.FilesDir); err != nil {
			r.AddOnError(c, err)
			return
		}

		contents, err := ioutil.ReadFile(filePath)
		if err != nil {
			r.AddOnError(c, err)
			return
		}

		src, gzipped, err := baseutil.MakeDataURL(contents, to.Compression, !options.NoResourceAutoCompression)
		if err != nil {
			r.AddOnError(c, err)
			return
		}
		to.Source = &src
		tm.AddTranslation(c, path.New("json", "source"))
		if gzipped {
			to.Compression = util.StrToPtr("gzip")
			tm.AddTranslation(c, path.New("json", "compression"))
		}
	}

	if from.Inline != nil {
		c := path.New("yaml", "inline")

		src, gzipped, err := baseutil.MakeDataURL([]byte(*from.Inline), to.Compression, !options.NoResourceAutoCompression)
		if err != nil {
			r.AddOnError(c, err)
			return
		}
		to.Source = &src
		tm.AddTranslation(c, path.New("json", "source"))
		if gzipped {
			to.Compression = util.StrToPtr("gzip")
			tm.AddTranslation(c, path.New("json", "compression"))
		}
	}
	return
}

func translateDirectory(from Directory, options common.TranslateOptions) (to types.Directory, tm translate.TranslationSet, r report.Report) {
	tr := translate.NewTranslator("yaml", "json", options)
	tm, r = translate.Prefixed(tr, "group", &from.Group, &to.Group)
	translate.MergeP(tr, tm, &r, "user", &from.User, &to.User)
	translate.MergeP(tr, tm, &r, "overwrite", &from.Overwrite, &to.Overwrite)
	translate.MergeP(tr, tm, &r, "path", &from.Path, &to.Path)
	translate.MergeP(tr, tm, &r, "mode", &from.Mode, &to.Mode)
	return
}

func translateLink(from Link, options common.TranslateOptions) (to types.Link, tm translate.TranslationSet, r report.Report) {
	tr := translate.NewTranslator("yaml", "json", options)
	tm, r = translate.Prefixed(tr, "group", &from.Group, &to.Group)
	translate.MergeP(tr, tm, &r, "user", &from.User, &to.User)
	translate.MergeP(tr, tm, &r, "target", &from.Target, &to.Target)
	translate.MergeP(tr, tm, &r, "hard", &from.Hard, &to.Hard)
	translate.MergeP(tr, tm, &r, "overwrite", &from.Overwrite, &to.Overwrite)
	translate.MergeP(tr, tm, &r, "path", &from.Path, &to.Path)
	return
}

func (c Config) processTrees(ret *types.Config, options common.TranslateOptions) (translate.TranslationSet, report.Report) {
	ts := translate.NewTranslationSet("yaml", "json")
	var r report.Report
	if len(c.Storage.Trees) == 0 {
		return ts, r
	}
	t := newNodeTracker(ret)

	for i, tree := range c.Storage.Trees {
		yamlPath := path.New("yaml", "storage", "trees", i)
		if options.FilesDir == "" {
			r.AddOnError(yamlPath, common.ErrNoFilesDir)
			return ts, r
		}

		// calculate base path within FilesDir and check for
		// path traversal
		srcBaseDir := filepath.Join(options.FilesDir, tree.Local)
		if err := baseutil.EnsurePathWithinFilesDir(srcBaseDir, options.FilesDir); err != nil {
			r.AddOnError(yamlPath, err)
			continue
		}
		info, err := os.Stat(srcBaseDir)
		if err != nil {
			r.AddOnError(yamlPath, err)
			continue
		}
		if !info.IsDir() {
			r.AddOnError(yamlPath, common.ErrTreeNotDirectory)
			continue
		}
		destBaseDir := "/"
		if util.NotEmpty(tree.Path) {
			destBaseDir = *tree.Path
		}

		walkTree(yamlPath, tree, &ts, &r, t, srcBaseDir, destBaseDir, options)
	}
	return ts, r
}

func walkTree(yamlPath path.ContextPath, tree Tree, ts *translate.TranslationSet, r *report.Report, t *nodeTracker, srcBaseDir, destBaseDir string, options common.TranslateOptions) {
	// The strategy for errors within WalkFunc is to add an error to
	// the report and return nil, so walking continues but translation
	// will fail afterward.
	err := filepath.Walk(srcBaseDir, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			r.AddOnError(yamlPath, err)
			return nil
		}
		relPath, err := filepath.Rel(srcBaseDir, srcPath)
		if err != nil {
			r.AddOnError(yamlPath, err)
			return nil
		}
		destPath := filepath.Join(destBaseDir, relPath)

		if info.Mode().IsDir() {
			return nil
		} else if info.Mode().IsRegular() {
			i, file := t.GetFile(destPath)
			if file != nil {
				if util.NotEmpty(file.Contents.Source) {
					r.AddOnError(yamlPath, common.ErrNodeExists)
					return nil
				}
			} else {
				if t.Exists(destPath) {
					r.AddOnError(yamlPath, common.ErrNodeExists)
					return nil
				}
				i, file = t.AddFile(types.File{
					Node: types.Node{
						Path: destPath,
					},
				})
				ts.AddFromCommonSource(yamlPath, path.New("json", "storage", "files", i), file)
				if i == 0 {
					ts.AddTranslation(yamlPath, path.New("json", "storage", "files"))
				}
			}
			contents, err := ioutil.ReadFile(srcPath)
			if err != nil {
				r.AddOnError(yamlPath, err)
				return nil
			}
			url, gzipped, err := baseutil.MakeDataURL(contents, file.Contents.Compression, !options.NoResourceAutoCompression)
			if err != nil {
				r.AddOnError(yamlPath, err)
				return nil
			}
			file.Contents.Source = util.StrToPtr(url)
			ts.AddTranslation(yamlPath, path.New("json", "storage", "files", i, "contents", "source"))
			if gzipped {
				file.Contents.Compression = util.StrToPtr("gzip")
				ts.AddTranslation(yamlPath, path.New("json", "storage", "files", i, "contents", "compression"))
			}
			ts.AddTranslation(yamlPath, path.New("json", "storage", "files", i, "contents"))
			if file.Mode == nil {
				mode := 0644
				if info.Mode()&0111 != 0 {
					mode = 0755
				}
				file.Mode = &mode
				ts.AddTranslation(yamlPath, path.New("json", "storage", "files", i, "mode"))
			}
		} else if info.Mode()&os.ModeType == os.ModeSymlink {
			i, link := t.GetLink(destPath)
			if link != nil {
				if util.NotEmpty(link.Target) {
					r.AddOnError(yamlPath, common.ErrNodeExists)
					return nil
				}
			} else {
				if t.Exists(destPath) {
					r.AddOnError(yamlPath, common.ErrNodeExists)
					return nil
				}
				i, link = t.AddLink(types.Link{
					Node: types.Node{
						Path: destPath,
					},
				})
				ts.AddFromCommonSource(yamlPath, path.New("json", "storage", "links", i), link)
				if i == 0 {
					ts.AddTranslation(yamlPath, path.New("json", "storage", "links"))
				}
			}
			target, err := os.Readlink(srcPath)
			if err != nil {
				r.AddOnError(yamlPath, err)
				return nil
			}
			link.Target = &target
			ts.AddTranslation(yamlPath, path.New("json", "storage", "links", i, "target"))
		} else {
			r.AddOnError(yamlPath, common.ErrFileType)
			return nil
		}
		return nil
	})
	r.AddOnError(yamlPath, err)
}

func (c Config) addMountUnits(config *types.Config, ts *translate.TranslationSet) {
	if len(c.Storage.Filesystems) == 0 {
		return
	}
	var rendered types.Config
	renderedTranslations := translate.NewTranslationSet("yaml", "json")
	renderedTranslations.AddTranslation(path.New("yaml", "storage", "filesystems"), path.New("json", "systemd"))
	renderedTranslations.AddTranslation(path.New("yaml", "storage", "filesystems"), path.New("json", "systemd", "units"))
	for i, fs := range c.Storage.Filesystems {
		if !util.IsTrue(fs.WithMountUnit) {
			continue
		}
		fromPath := path.New("yaml", "storage", "filesystems", i, "with_mount_unit")
		remote := false
		// check filesystems targeting /dev/mapper devices against LUKS to determine if a
		// remote mount is needed
		if strings.HasPrefix(fs.Device, "/dev/mapper/") || strings.HasPrefix(fs.Device, "/dev/disk/by-id/dm-name-") {
			for _, luks := range c.Storage.Luks {
				// LUKS devices are opened with their name specified
				if fs.Device == fmt.Sprintf("/dev/mapper/%s", luks.Name) || fs.Device == fmt.Sprintf("/dev/disk/by-id/dm-name-%s", luks.Name) {
					if len(luks.Clevis.Tang) > 0 {
						remote = true
						break
					}
				}
			}
		}
		newUnit := mountUnitFromFS(fs, remote)
		unitPath := path.New("json", "systemd", "units", len(rendered.Systemd.Units))
		rendered.Systemd.Units = append(rendered.Systemd.Units, newUnit)
		renderedTranslations.AddFromCommonSource(fromPath, unitPath, newUnit)
	}
	retConfig, retTranslations := baseutil.MergeTranslatedConfigs(rendered, renderedTranslations, *config, *ts)
	*config = retConfig.(types.Config)
	*ts = retTranslations
}

func mountUnitFromFS(fs Filesystem, remote bool) types.Unit {
	context := struct {
		*Filesystem
		EscapedDevice string
		Remote        bool
		Swap          bool
	}{
		Filesystem:    &fs,
		EscapedDevice: unit.UnitNamePathEscape(fs.Device),
		Remote:        remote,
		// unchecked deref of format ok, fs would fail validation otherwise
		Swap: *fs.Format == "swap",
	}
	contents := strings.Builder{}
	err := mountUnitTemplate.Execute(&contents, context)
	if err != nil {
		panic(err)
	}
	var unitName string
	if context.Swap {
		unitName = unit.UnitNamePathEscape(fs.Device) + ".swap"
	} else {
		// unchecked deref of path ok, fs would fail validation otherwise
		unitName = unit.UnitNamePathEscape(*fs.Path) + ".mount"
	}
	return types.Unit{
		Name:     unitName,
		Enabled:  util.BoolToPtr(true),
		Contents: util.StrToPtr(contents.String()),
	}
}
