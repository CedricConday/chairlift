package installcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/projectbluefin/chairlift/internal/imageinfo"
	"github.com/projectbluefin/chairlift/internal/updex"
	"gopkg.in/yaml.v3"
)

// wantSPDXLicense is the SPDX identifier the project's actual license (GPLv3
// text in LICENSE, gtk.LicenseGpl30Value — the GTK bindings' "GPL 3.0 or later"
// enum value — in internal/window/window.go's about dialog) resolves to.
// Every nfpms[] entry's license must agree with this constant; see
// docs/design/package-managers.md's "Install-path consistency" section for the
// MIT/GPL drift bug this guards against.
const wantSPDXLicense = "GPL-3.0-or-later"

// wantHomepage is the repository URL release.footer's "Full Changelog" line
// must contain. GoReleaser OSS (unlike Pro) has no metadata.homepage to
// template from, so the URL is a literal in the footer text itself — this
// constant is what TestGoreleaserReleaseFooterHasCanonicalRepoURL checks it
// against.
const wantHomepage = "https://github.com/projectbluefin/chairlift"

const (
	fullPackageName        = "projectbluefin-chairlift"
	integrationPackageName = "projectbluefin-chairlift-system-integration"
)

// loadGoreleaserConfig parses the real, repo-root .goreleaser.yaml — not a
// fixture or copy that could drift from the file goreleaser actually reads
// — using the yaml.v3 dependency already vendored for internal/config.
func loadGoreleaserConfig(t *testing.T) GoreleaserConfig {
	t.Helper()

	path := filepath.Join(RepoRoot(), ".goreleaser.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var cfg GoreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return cfg
}

// nfpmContentBySuffix finds the contents[] entry whose src ends in srcSuffix,
// failing the test if no such entry exists.
func nfpmContentBySuffix(t *testing.T, nfpm NfpmConfig, srcSuffix string) NfpmContent {
	t.Helper()

	for _, c := range nfpm.Contents {
		if strings.HasSuffix(c.Src, srcSuffix) {
			return c
		}
	}
	t.Fatalf("no nfpm contents entry with src ending in %q", srcSuffix)
	return NfpmContent{}
}

func nfpmDst(t *testing.T, nfpm NfpmConfig, srcSuffix string) string {
	t.Helper()
	return nfpmContentBySuffix(t, nfpm, srcSuffix).Dst
}

func nfpmByPackageName(t *testing.T, cfg GoreleaserConfig, packageName string) NfpmConfig {
	t.Helper()

	var matches []NfpmConfig
	for _, nfpm := range cfg.Nfpms {
		if nfpm.PackageName == packageName {
			matches = append(matches, nfpm)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("nfpms entries with package_name %q = %d, want exactly 1", packageName, len(matches))
	}
	return matches[0]
}

// TestGoreleaserNfpmLayoutMatchesUsrPrefix parses the real .goreleaser.yaml
// and asserts its nFPM package layout agrees with internal/updex.HelperPath
// and the fixed PolicyKit read locations, so a future edit to any one of
// .goreleaser.yaml, internal/updex.HelperPath, or the polkit directories
// fails this test instead of silently drifting.
//
// It iterates every nfpms[] entry rather than only nfpms[0]: the acceptance
// criterion is a consistency invariant across the whole nFPM block, so adding
// or reordering a second package with the wrong bindir or polkit destinations
// must fail here too (per
// docs/agents/skills/regression-tests-must-cover-every-collection-entry.md).
func TestGoreleaserNfpmLayoutMatchesUsrPrefix(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Nfpms) == 0 {
		t.Fatal(".goreleaser.yaml has no nfpms entries")
	}

	// nFPM auto-packages each entry's selected build ids into bindir. Every
	// current package selects chairlift-updex-helper, so bindir determines
	// where its helper lands. It must match the directory of
	// internal/updex.HelperPath — the fixed absolute path pkexec matches
	// against the PolicyKit policy's exec.path annotation — not just a
	// hardcoded "/usr/bin" literal, so this fails if either side changes
	// without the other.
	wantBindir := filepath.Dir(updex.HelperPath)

	contentChecks := []struct {
		name      string
		srcSuffix string
		want      string
	}{
		{"maintainer config", "config.yml", "/usr/share/chairlift/config.yml"},
		{"updex policy", "io.projectbluefin.chairlift.updex.policy", filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.updex.policy")},
		{"bootc policy", "io.projectbluefin.chairlift.bootc.policy", filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.bootc.policy")},
		{"sysupdate policy", "io.projectbluefin.chairlift.sysupdate.policy", filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.sysupdate.policy")},
		{"ublue policy", "io.projectbluefin.chairlift.ublue.policy", filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.ublue.policy")},
		{"channel table example", "channels.example.yml", "/usr/share/doc/chairlift/channels.example.yml"},
	}

	for i, nfpm := range cfg.Nfpms {
		t.Run(fmt.Sprintf("nfpms[%d]", i), func(t *testing.T) {
			if nfpm.Bindir != wantBindir {
				t.Errorf("nfpms[%d].bindir = %q, want %q (must match the directory of internal/updex.HelperPath)", i, nfpm.Bindir, wantBindir)
			}

			for _, cc := range contentChecks {
				t.Run(cc.name, func(t *testing.T) {
					content := nfpmContentBySuffix(t, nfpm, cc.srcSuffix)
					if content.Dst != cc.want {
						t.Errorf("nfpms[%d] contents dst for %s = %q, want required package path %q", i, cc.srcSuffix, content.Dst, cc.want)
					}
					if content.FileInfo.Mode != 0o644 {
						t.Errorf("nfpms[%d] contents mode for %s = %#o, want 0644", i, cc.srcSuffix, content.FileInfo.Mode)
					}
				})
			}

			for _, content := range nfpm.Contents {
				if content.Dst == "/etc/chairlift/config.yml" {
					t.Errorf("nfpms[%d] packages administrator-owned /etc/chairlift/config.yml", i)
				}
				// The channel table decides the image reference the
				// privileged helper hands to bootc. Packaging a live table
				// at either read path would install a switch mapping the
				// administrator never chose; only the documented example
				// under /usr/share/doc is shipped.
				for _, live := range imageinfo.SystemTablePaths {
					if content.Dst == live {
						t.Errorf("nfpms[%d] packages a live channel table at %s; ship only the example", i, live)
					}
				}
				if strings.HasSuffix(content.Src, ".rules") || strings.HasSuffix(content.Dst, ".rules") {
					t.Errorf("nfpms[%d] still packages passwordless PolicyKit rule: src=%q dst=%q", i, content.Src, content.Dst)
				}
			}
		})
	}
}

func TestGoreleaserPublishesSystemIntegrationPackage(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	full := nfpmByPackageName(t, cfg, fullPackageName)
	integration := nfpmByPackageName(t, cfg, integrationPackageName)

	if full.ID == "" || integration.ID == "" || full.ID == integration.ID {
		t.Errorf("nFPM ids must be non-empty and unique: full=%q integration=%q", full.ID, integration.ID)
	}
	if want := []string{"chairlift", "chairlift-updex-helper", "chairlift-ublue-helper"}; !reflect.DeepEqual(full.IDs, want) {
		t.Errorf("%s ids = %v, want %v", fullPackageName, full.IDs, want)
	}
	if want := []string{"chairlift-updex-helper", "chairlift-ublue-helper"}; !reflect.DeepEqual(integration.IDs, want) {
		t.Errorf("%s ids = %v, want %v", integrationPackageName, integration.IDs, want)
	}
	if want := []string{integrationPackageName}; !reflect.DeepEqual(full.Conflicts, want) {
		t.Errorf("%s conflicts = %v, want %v", fullPackageName, full.Conflicts, want)
	}
	if want := []string{fullPackageName}; !reflect.DeepEqual(integration.Conflicts, want) {
		t.Errorf("%s conflicts = %v, want %v", integrationPackageName, integration.Conflicts, want)
	}
	for _, nfpm := range []NfpmConfig{full, integration} {
		if want := []string{"deb", "rpm", "apk"}; !reflect.DeepEqual(nfpm.Formats, want) {
			t.Errorf("%s formats = %v, want %v", nfpm.PackageName, nfpm.Formats, want)
		}
	}

	wantIntegrationContents := map[string]string{
		"config.yml": "/usr/share/chairlift/config.yml",
		"io.projectbluefin.chairlift.bootc.policy":     filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.bootc.policy"),
		"io.projectbluefin.chairlift.updex.policy":     filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.updex.policy"),
		"io.projectbluefin.chairlift.sysupdate.policy": filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.sysupdate.policy"),
		"io.projectbluefin.chairlift.ublue.policy":     filepath.Join(polkitActionsDir, "io.projectbluefin.chairlift.ublue.policy"),
		"channels.example.yml":                         "/usr/share/doc/chairlift/channels.example.yml",
	}
	if len(integration.Contents) != len(wantIntegrationContents) {
		t.Errorf("%s contents has %d entries, want %d", integrationPackageName, len(integration.Contents), len(wantIntegrationContents))
	}
	for srcSuffix, wantDst := range wantIntegrationContents {
		if got := nfpmDst(t, integration, srcSuffix); got != wantDst {
			t.Errorf("%s contents dst for %s = %q, want %q", integrationPackageName, srcSuffix, got, wantDst)
		}
	}
}

// TestGoreleaserLicenseIsGPL parses the real .goreleaser.yaml and asserts
// every nfpms[] entry's license equals wantSPDXLicense, so a future edit
// reverting it back to MIT (or any other value) — as happened before this
// test was added — fails this test instead of silently shipping mislabeled
// deb/rpm/apk packages.
//
// It iterates every nfpms[] entry rather than only nfpms[0]: per
// docs/agents/skills/regression-tests-must-cover-every-collection-entry.md,
// a consistency check that only special-cases the first entry stops
// protecting anything the moment a second nfpms[] entry is added or
// reordered with the wrong license.
func TestGoreleaserLicenseIsGPL(t *testing.T) {
	cfg := loadGoreleaserConfig(t)

	if len(cfg.Nfpms) == 0 {
		t.Fatal(".goreleaser.yaml has no nfpms entries")
	}

	for i, nfpm := range cfg.Nfpms {
		t.Run(fmt.Sprintf("nfpms[%d]", i), func(t *testing.T) {
			if nfpm.License != wantSPDXLicense {
				t.Errorf("nfpms[%d].license = %q, want %q", i, nfpm.License, wantSPDXLicense)
			}
		})
	}
}

// TestGoreleaserReleaseFooterHasCanonicalRepoURL parses the real
// .goreleaser.yaml and asserts release.footer's "Full Changelog" line
// contains the canonical repository URL (wantHomepage). GoReleaser OSS has
// no metadata.homepage to template the URL from — unlike Pro — so the URL
// is necessarily a literal here; this test is what catches it drifting from
// the actual repository location instead.
//
// The assertions are on the raw *template text* read from the YAML: the
// footer is expanded by goreleaser at release time, and goreleaser is not
// installed on the gate host or in make ci, so nothing here renders the
// template or shells out.
//
// It is deliberately non-vacuous: an absent or empty footer, or zero or more
// than one "Full Changelog" line, is a t.Fatal rather than a silent pass.
func TestGoreleaserReleaseFooterHasCanonicalRepoURL(t *testing.T) {
	cfg := loadGoreleaserConfig(t)

	footer := cfg.Release.Footer
	if strings.TrimSpace(footer) == "" {
		t.Fatal("release.footer is absent or empty in .goreleaser.yaml")
	}

	var matches []string
	for _, line := range strings.Split(footer, "\n") {
		if strings.Contains(line, "Full Changelog") {
			matches = append(matches, line)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("release.footer has %d lines containing %q, want exactly 1; footer:\n%s", len(matches), "Full Changelog", footer)
	}
	line := matches[0]

	if !strings.Contains(line, wantHomepage) {
		t.Errorf("Full Changelog line = %q, want it to contain the canonical repository URL %q", line, wantHomepage)
	}
	if !strings.Contains(line, "/compare/{{ .PreviousTag }}...{{ .Tag }}") {
		t.Errorf("Full Changelog line = %q, want it to keep the %q suffix", line, "/compare/{{ .PreviousTag }}...{{ .Tag }}")
	}
	if strings.Contains(line, ".ProjectName") {
		t.Errorf("Full Changelog line = %q, want no .ProjectName: the repository URL already ends in the repository name, so the two must not be concatenated", line)
	}
}

// guiBuildID is the .goreleaser.yaml builds[] id of the GTK application
// binary, and wrapperSrcSuffix the source path of the Bash launcher the
// packaged desktop entry's Exec= line runs. A package selecting the first or
// shipping the second is a package that cannot start without GTK 4,
// libadwaita 1, and Bash present on the target system.
const (
	guiBuildID       = "chairlift"
	wrapperSrcSuffix = "chairlift-wrapper.sh"
	desktopSrcSuffix = "io.projectbluefin.chairlift.desktop"
)

// wantRuntimeDependencies are the mandatory runtime dependencies every
// GUI-shipping nfpms[] entry must declare, with the package name each output
// format's distro family uses for them.
//
// None of these is discoverable by nFPM's automatic dependency scanning: the
// binaries are built with CGO_ENABLED=0 and link nothing, puregotk resolves
// GTK and libadwaita with dlopen at runtime (core.SetSharedLibraries names
// "libgtk-4.so.1" and "libadwaita-1.so.0"), and the Bash requirement comes
// from the packaged wrapper's interpreter line, not from linkage. An
// undeclared dependency therefore does not fail the build or the package —
// it installs cleanly on a minimal image and fails at launch, which is the
// regression this test exists to catch.
var wantRuntimeDependencies = []struct {
	what     string
	byFormat map[string]string
}{
	{
		what:     "Bash, the interpreter of the wrapper the desktop entry execs",
		byFormat: map[string]string{"deb": "bash", "rpm": "bash", "apk": "bash"},
	},
	{
		what:     `GTK 4, dlopened as "libgtk-4.so.1"`,
		byFormat: map[string]string{"deb": "libgtk-4-1", "rpm": "gtk4", "apk": "gtk4.0"},
	},
	{
		what:     `libadwaita 1, dlopened as "libadwaita-1.so.0"`,
		byFormat: map[string]string{"deb": "libadwaita-1-0", "rpm": "libadwaita", "apk": "libadwaita"},
	},
}

// nfpmShipsGUI reports whether an nfpms[] entry ships the GTK application:
// either by selecting its build id, or by installing the wrapper or desktop
// entry that launches it. It is derived from the parsed config rather than
// from a package-name literal so that a renamed or newly added GUI package
// is covered by TestGoreleaserNfpmDeclaresRuntimeDependencies automatically.
func nfpmShipsGUI(nfpm NfpmConfig) bool {
	for _, id := range nfpm.IDs {
		if id == guiBuildID {
			return true
		}
	}
	for _, c := range nfpm.Contents {
		if strings.HasSuffix(c.Src, wrapperSrcSuffix) || strings.HasSuffix(c.Src, desktopSrcSuffix) {
			return true
		}
	}
	return false
}

// TestGoreleaserNfpmDeclaresRuntimeDependencies parses the real, repo-root
// .goreleaser.yaml and asserts every GUI-shipping nfpms[] entry declares its
// mandatory runtime dependencies for every format it builds, under that
// format's own distro-family package names.
//
// It iterates every nfpms[] entry and every entry's formats[] rather than
// checking nfpms[0] and "deb" — per
// docs/skills/collection-regressions/SKILL.md, a consistency check that
// special-cases one member stops protecting anything as soon as a second one
// is added. A GUI package that builds a format with no known package names
// is a t.Fatal, not a skip: a new output format needs its names added here
// deliberately, not inherited silently from the top-level list.
//
// Non-GUI packages (the root-owned system-integration companion, which ships
// only the privileged helpers and their PolicyKit policies) are exempt and
// recorded as such, so the test does not quietly pass by matching nothing.
func TestGoreleaserNfpmDeclaresRuntimeDependencies(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Nfpms) == 0 {
		t.Fatal(".goreleaser.yaml has no nfpms entries")
	}

	guiPackages := 0
	for i, nfpm := range cfg.Nfpms {
		if !nfpmShipsGUI(nfpm) {
			continue
		}
		guiPackages++

		t.Run(fmt.Sprintf("nfpms[%d]/%s", i, nfpm.PackageName), func(t *testing.T) {
			if len(nfpm.Formats) == 0 {
				t.Fatalf("%s declares no formats", nfpm.PackageName)
			}
			for _, format := range nfpm.Formats {
				t.Run(format, func(t *testing.T) {
					deps := nfpm.DependenciesFor(format)
					if len(deps) == 0 {
						t.Fatalf("%s declares no runtime dependencies for %s; a minimal target installs it and then cannot launch", nfpm.PackageName, format)
					}
					for _, want := range wantRuntimeDependencies {
						pkg, ok := want.byFormat[format]
						if !ok {
							t.Fatalf("no %s package name known for %s; add one to wantRuntimeDependencies rather than letting the format inherit another family's names", format, want.what)
						}
						if !slices.Contains(deps, pkg) {
							t.Errorf("%s dependencies for %s = %v, want it to contain %q (%s)", nfpm.PackageName, format, deps, pkg, want.what)
						}
					}
				})
			}
		})
	}

	if guiPackages == 0 {
		t.Fatal("no nfpms[] entry ships the GTK application; this test would pass vacuously")
	}
}
