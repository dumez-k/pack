// +build acceptance

package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	acceptanceOS "github.com/buildpacks/pack/acceptance/os"
	"github.com/buildpacks/pack/internal/api"
	"github.com/buildpacks/pack/internal/blob"
	"github.com/buildpacks/pack/internal/builder"
	"github.com/buildpacks/pack/internal/style"
	h "github.com/buildpacks/pack/testhelpers"
)

const (
	defaultCompilePackVersion = "0.0.0"
	defaultPlatformAPIVersion = "0.3"
)

var (
	currentPackFixturesDir           = filepath.Join("testdata", "pack_fixtures")
	previousPackFixturesOverridesDir = filepath.Join("testdata", "pack_previous_fixtures_overrides")
	githubAssetFetcher               *GithubAssetFetcher
	lifecycleTgzExp                  = regexp.MustCompile(`lifecycle-v\d+.\d+.\d+\+linux.x86-64.tgz`)
)

type AssetManager struct {
	packPath                    string
	packFixturesPath            string
	previousPackPath            string
	previousPackFixturesPaths   []string
	lifecyclePath               string
	lifecycleDescriptor         builder.LifecycleDescriptor
	previousLifecyclePath       string
	previousLifecycleDescriptor builder.LifecycleDescriptor
	defaultLifecycleDescriptor  builder.LifecycleDescriptor
	testObject                  *testing.T
}

func ConvergedAssetManager(t *testing.T, inputConfig InputConfigurationManager) AssetManager {
	t.Helper()

	var (
		convergedCurrentPackPath             string
		convergedPreviousPackPath            string
		convergedPreviousPackFixturesPaths   []string
		convergedCurrentLifecyclePath        string
		convergedCurrentLifecycleDescriptor  builder.LifecycleDescriptor
		convergedPreviousLifecyclePath       string
		convergedPreviousLifecycleDescriptor builder.LifecycleDescriptor
		convergedDefaultLifecycleDescriptor  builder.LifecycleDescriptor
	)

	assetBuilder := assetManagerBuilder{
		testObject:  t,
		inputConfig: inputConfig,
	}

	if inputConfig.combinations.requiresCurrentPack() {
		convergedCurrentPackPath = assetBuilder.ensureCurrentPack()
	}

	if inputConfig.combinations.requiresPreviousPack() {
		convergedPreviousPackPath = assetBuilder.ensurePreviousPack()
		convergedPreviousPackFixturesPath := assetBuilder.ensurePreviousPackFixtures()

		convergedPreviousPackFixturesPaths = []string{previousPackFixturesOverridesDir, convergedPreviousPackFixturesPath}
	}

	if inputConfig.combinations.requiresCurrentLifecycle() {
		convergedCurrentLifecyclePath, convergedCurrentLifecycleDescriptor = assetBuilder.ensureCurrentLifecycle()
	}

	if inputConfig.combinations.requiresPreviousLifecycle() {
		convergedPreviousLifecyclePath, convergedPreviousLifecycleDescriptor = assetBuilder.ensurePreviousLifecycle()
	}

	if inputConfig.combinations.requiresDefaultLifecycle() {
		convergedDefaultLifecycleDescriptor = defaultLifecycleDescriptor()
	}

	return AssetManager{
		packPath:                    convergedCurrentPackPath,
		packFixturesPath:            currentPackFixturesDir,
		previousPackPath:            convergedPreviousPackPath,
		previousPackFixturesPaths:   convergedPreviousPackFixturesPaths,
		lifecyclePath:               convergedCurrentLifecyclePath,
		lifecycleDescriptor:         convergedCurrentLifecycleDescriptor,
		previousLifecyclePath:       convergedPreviousLifecyclePath,
		previousLifecycleDescriptor: convergedPreviousLifecycleDescriptor,
		defaultLifecycleDescriptor:  convergedDefaultLifecycleDescriptor,
		testObject:                  t,
	}
}

func (a AssetManager) PackPaths(kind ComboValue) (packPath string, packFixturesPaths []string) {
	a.testObject.Helper()

	switch kind {
	case Current:
		packPath = a.packPath
		packFixturesPaths = []string{a.packFixturesPath}
	case Previous:
		packPath = a.previousPackPath
		packFixturesPaths = a.previousPackFixturesPaths
	default:
		a.testObject.Fatalf("pack kind must be current or previous, was %s", kind)
	}

	return packPath, packFixturesPaths
}

func (a AssetManager) LifecyclePath(kind ComboValue) string {
	a.testObject.Helper()

	switch kind {
	case Current:
		return a.lifecyclePath
	case Previous:
		return a.previousLifecyclePath
	case DefaultKind:
		return ""
	}

	a.testObject.Fatalf("lifecycle kind must be previous, current or default was %s", kind)
	return "" // Unreachable
}

func (a AssetManager) LifecycleDescriptor(kind ComboValue) builder.LifecycleDescriptor {
	a.testObject.Helper()

	switch kind {
	case Current:
		return a.lifecycleDescriptor
	case Previous:
		return a.previousLifecycleDescriptor
	case DefaultKind:
		return a.defaultLifecycleDescriptor
	}

	a.testObject.Fatalf("lifecycle kind must be previous, current or default was %s", kind)
	return builder.LifecycleDescriptor{} // Unreachable
}

type assetManagerBuilder struct {
	testObject  *testing.T
	inputConfig InputConfigurationManager
}

func (b assetManagerBuilder) ensureCurrentPack() string {
	b.testObject.Helper()

	if b.inputConfig.packPath != "" {
		return b.inputConfig.packPath
	}

	compileWithVersion := b.inputConfig.compilePackWithVersion
	if compileWithVersion == "" {
		compileWithVersion = defaultCompilePackVersion
	}

	return b.buildPack(compileWithVersion)
}

func (b assetManagerBuilder) ensurePreviousPack() string {
	b.testObject.Helper()

	if b.inputConfig.previousPackPath != "" {
		return b.inputConfig.previousPackPath
	}

	b.testObject.Logf(
		"run combinations %+v require %s to be set",
		b.inputConfig.combinations,
		style.Symbol(envPreviousPackPath),
	)

	b.ensureGithubAssetFetcher()
	version, err := githubAssetFetcher.FetchReleaseVersion("buildpacks", "pack", 0)
	h.AssertNil(b.testObject, err)

	assetDir, err := githubAssetFetcher.FetchReleaseAsset(
		"buildpacks",
		"pack",
		version,
		acceptanceOS.PackBinaryExp,
		true,
	)
	h.AssertNil(b.testObject, err)
	assetPath := filepath.Join(assetDir, acceptanceOS.PackBinaryName)

	b.testObject.Logf("using %s for previous pack path", assetPath)

	return assetPath
}

func (b assetManagerBuilder) ensurePreviousPackFixtures() string {
	b.testObject.Helper()

	if b.inputConfig.previousPackFixturesPath != "" {
		return b.inputConfig.previousPackFixturesPath
	}

	b.testObject.Logf(
		"run combinations %+v require %s to be set",
		b.inputConfig.combinations,
		style.Symbol(envPreviousPackFixturesPath),
	)

	b.ensureGithubAssetFetcher()
	version, err := githubAssetFetcher.FetchReleaseVersion("buildpacks", "pack", 0)
	h.AssertNil(b.testObject, err)

	sourceDir, err := githubAssetFetcher.FetchReleaseSource("buildpacks", "pack", version)
	h.AssertNil(b.testObject, err)

	sourceDirFiles, err := ioutil.ReadDir(sourceDir)
	h.AssertNil(b.testObject, err)
	// GitHub source tarballs have a top-level directory whose name includes the current commit sha.
	innerDir := sourceDirFiles[0].Name()
	fixturesDir := filepath.Join(sourceDir, innerDir, "acceptance", "testdata", "pack_fixtures")

	b.testObject.Logf("using %s for previous pack fixtures path", fixturesDir)

	return fixturesDir
}

func (b assetManagerBuilder) ensureCurrentLifecycle() (string, builder.LifecycleDescriptor) {
	b.testObject.Helper()

	lifecyclePath := b.inputConfig.lifecyclePath

	if lifecyclePath == "" {
		b.testObject.Logf(
			"run combinations %+v require %s to be set",
			b.inputConfig.combinations,
			style.Symbol(envLifecyclePath),
		)

		lifecyclePath = b.downloadLifecycle(0)

		b.testObject.Logf("using %s for default lifecycle path", lifecyclePath)
	}

	lifecycle, err := builder.NewLifecycle(blob.NewBlob(lifecyclePath))
	h.AssertNil(b.testObject, err)

	return lifecyclePath, lifecycle.Descriptor()
}

func (b assetManagerBuilder) ensurePreviousLifecycle() (string, builder.LifecycleDescriptor) {
	b.testObject.Helper()

	previousLifecyclePath := b.inputConfig.previousLifecyclePath

	if previousLifecyclePath == "" {
		b.testObject.Logf(
			"run combinations %+v require %s to be set",
			b.inputConfig.combinations,
			style.Symbol(envPreviousLifecyclePath),
		)

		previousLifecyclePath = b.downloadLifecycle(-1)

		b.testObject.Logf("using %s for previous lifecycle path", previousLifecyclePath)
	}

	lifecycle, err := builder.NewLifecycle(blob.NewBlob(previousLifecyclePath))
	h.AssertNil(b.testObject, err)

	return previousLifecyclePath, lifecycle.Descriptor()
}

func (b assetManagerBuilder) downloadLifecycle(relativeVersion int) string {
	b.testObject.Helper()

	b.ensureGithubAssetFetcher()

	version, err := githubAssetFetcher.FetchReleaseVersion("buildpacks", "lifecycle", relativeVersion)
	h.AssertNil(b.testObject, err)

	path, err := githubAssetFetcher.FetchReleaseAsset(
		"buildpacks",
		"lifecycle",
		version,
		lifecycleTgzExp,
		false,
	)
	h.AssertNil(b.testObject, err)

	return path
}

func (b assetManagerBuilder) ensureGithubAssetFetcher() {
	b.testObject.Helper()

	if githubAssetFetcher != nil {
		return
	}

	var err error
	githubAssetFetcher, err = NewGithubAssetFetcher(b.testObject, b.inputConfig.githubToken)
	h.AssertNil(b.testObject, err)
}

func (b assetManagerBuilder) buildPack(compileVersion string) string {
	b.testObject.Helper()

	packTmpDir, err := ioutil.TempDir("", "pack.acceptance.binary.")
	h.AssertNil(b.testObject, err)

	packPath := filepath.Join(packTmpDir, acceptanceOS.PackBinaryName)

	cwd, err := os.Getwd()
	h.AssertNil(b.testObject, err)

	cmd := exec.Command("go", "build",
		"-ldflags", fmt.Sprintf("-X 'github.com/buildpacks/pack/cmd.Version=%s'", compileVersion),
		"-mod=vendor",
		"-o", packPath,
		"./cmd/pack",
	)
	if filepath.Base(cwd) == "acceptance" {
		cmd.Dir = filepath.Dir(cwd)
	}

	b.testObject.Logf("building pack: [CWD=%s] %s", cmd.Dir, cmd.Args)
	_, err = cmd.CombinedOutput()
	h.AssertNil(b.testObject, err)

	return packPath
}

func defaultLifecycleDescriptor() builder.LifecycleDescriptor {
	return builder.LifecycleDescriptor{
		Info: builder.LifecycleInfo{
			Version: builder.VersionMustParse(builder.DefaultLifecycleVersion),
		},
		API: builder.LifecycleAPI{
			BuildpackVersion: api.MustParse(builder.DefaultBuildpackAPIVersion),
			PlatformVersion:  api.MustParse(defaultPlatformAPIVersion),
		},
	}
}
