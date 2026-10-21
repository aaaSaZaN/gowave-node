package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aaaSaZaN/gowave-node/internal/config"
	"github.com/aaaSaZaN/gowave-node/internal/updater"
	"github.com/aaaSaZaN/gowave-node/internal/xray"
)

func RunUpdateCLI(currentVersion string, args []string) {
	repo := updater.NodeRepository
	channel := "release"
	targetVersion := ""
	autoConfirm := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-y" || arg == "--yes" {
			autoConfirm = true
		} else if arg == "--channel" && i+1 < len(args) {
			channel = args[i+1]
			i++
		} else if (arg == "--version" || arg == "-v") && i+1 < len(args) {
			targetVersion = args[i+1]
			i++
		} else if (arg == "--repo" || arg == "-r") && i+1 < len(args) {
			repo = args[i+1]
			i++
		}
	}

	fmt.Printf("%s[UPDATE]%s Checking releases for %s...\n", colorCyan, colorReset, repo)
	releases, err := updater.FetchReleases(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Failed to fetch releases: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	if len(releases) == 0 {
		fmt.Println("No releases found.")
		return
	}

	var chosenRelease *updater.ReleaseInfo

	if targetVersion != "" {
		exact, err := updater.FetchRelease(repo, targetVersion)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		chosenRelease = &exact
	} else if !autoConfirm {
		fmt.Printf("\nCurrent version: %s%s%s (arch: %s/%s)\n\n", colorGreen, currentVersion, colorReset, updater.GetCurrentOS(), updater.AssetArch())
		if len(releases) == 0 {
			fmt.Fprintln(os.Stderr, "No releases found")
			os.Exit(1)
		}
		fmt.Println("Available releases:")
		var filtered []updater.ReleaseInfo
		for i, r := range releases {
			if i >= 6 {
				break
			}
			tag := "[Release]"
			if r.Prerelease {
				tag = "[Pre-release]"
			}
			fmt.Printf("  %d) %s %s (%s)\n", i+1, r.TagName, tag, r.Name)
			filtered = append(filtered, r)
		}

		fmt.Printf("\nSelect version [1-%d] (default: 1): ", len(filtered))
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		idx := 0
		if input != "" {
			var choice int
			if _, err := fmt.Sscanf(input, "%d", &choice); err == nil && choice >= 1 && choice <= len(filtered) {
				idx = choice - 1
			} else {
				fmt.Println("Invalid choice.")
				os.Exit(1)
			}
		}
		chosenRelease = &filtered[idx]
	} else {
		if channel == "prerelease" {
			for _, r := range releases {
				if r.Prerelease {
					chosenRelease = &r
					break
				}
			}
		} else if channel == "release" {
			exact, err := updater.FetchRelease(repo, "latest")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			chosenRelease = &exact
		} else {
			fmt.Fprintln(os.Stderr, "channel must be release or prerelease")
			os.Exit(1)
		}

	}

	if chosenRelease == nil {
		fmt.Println("No suitable release found.")
		return
	}

	fmt.Printf("\nTarget: %s%s%s (Prerelease: %v)\n", colorCyan, chosenRelease.TagName, colorReset, chosenRelease.Prerelease)

	if !autoConfirm {
		fmt.Printf("%s[WARNING]%s This operation will update the binary and restart gowave-node.\n", colorYellow, colorReset)
		fmt.Print("Proceed with update? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("Update aborted.")
			return
		}
	}

	asset, err := updater.MatchAsset(chosenRelease.Assets, updater.GetCurrentOS(), updater.AssetArch(), "gowave-node")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	fmt.Printf("Downloading %s (%.1f MB)...\n", asset.Name, float64(asset.Size)/(1024*1024))
	tmpPath, err := updater.DownloadReleaseBinary(*chosenRelease, *asset, "gowave-node")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Download failed: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	selfPath, err := os.Executable()
	if err != nil {
		selfPath = "/usr/local/bin/gowave-node"
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	fmt.Printf("Applying update to %s...\n", selfPath)
	if err := updater.ValidateNodeBinary(tmpPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	unlock, err := updater.LockTarget(selfPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer unlock()
	if err := updater.Backup(selfPath, ".previous"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := updater.AtomicReplace(tmpPath, selfPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Failed to replace binary: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	fmt.Printf("%s[SUCCESS]%s gowave-node updated to %s!\n", colorGreen, colorReset, chosenRelease.TagName)
	fmt.Println("Restarting service...")
	d, _ := config.ReadDiscovery()
	if err := CompleteUpdate(selfPath, Ready(true), d.PID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func RunCoreCLI(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: gowave-node core [command]")
		fmt.Println("\nCommands:")
		fmt.Println("  install [--repo XTLS/Xray-core] [--version v...] [-y]   Install or update Xray core")
		fmt.Println("  reset                                                  Reset to default core")
		return
	}

	sub := args[0]
	if sub == "reset" {
		path := xray.FindCorePath(false)
		if err := updater.ResetCore(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := updater.RestartNodeProcess(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if sub != "install" && sub != "update" {
		fmt.Println("Unknown command:", sub)
		return
	}

	repo := "XTLS/Xray-core"
	targetVersion := ""
	autoConfirm := false

	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-y" || arg == "--yes" {
			autoConfirm = true
		} else if (arg == "--repo" || arg == "-r") && i+1 < len(args) {
			repo = args[i+1]
			i++
		} else if (arg == "--version" || arg == "-v") && i+1 < len(args) {
			targetVersion = args[i+1]
			i++
		}
	}

	fmt.Printf("%s[CORE]%s Checking releases for %s...\n", colorCyan, colorReset, repo)
	releases, err := updater.FetchReleases(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Failed to fetch releases: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	var chosenRelease *updater.ReleaseInfo
	if targetVersion != "" {
		exact, err := updater.FetchRelease(repo, targetVersion)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		chosenRelease = &exact
	} else if !autoConfirm {
		if len(releases) == 0 {
			fmt.Fprintln(os.Stderr, "No releases found")
			os.Exit(1)
		}
		fmt.Println("Available releases:")
		var filtered []updater.ReleaseInfo
		for i, r := range releases {
			if i >= 6 {
				break
			}
			tag := "[Release]"
			if r.Prerelease {
				tag = "[Pre-release]"
			}
			fmt.Printf("  %d) %s %s\n", i+1, r.TagName, tag)
			filtered = append(filtered, r)
		}
		fmt.Printf("\nSelect core version [1-%d] (default: 1): ", len(filtered))
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		idx := 0
		if input != "" {
			var choice int
			if _, err := fmt.Sscanf(input, "%d", &choice); err == nil && choice >= 1 && choice <= len(filtered) {
				idx = choice - 1
			}
		}
		chosenRelease = &filtered[idx]
	} else {
		exact, err := updater.FetchRelease(repo, "latest")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		chosenRelease = &exact

	}

	if chosenRelease == nil {
		fmt.Println("No release found.")
		return
	}

	fmt.Printf("\nTarget Core: %s%s%s from %s\n", colorCyan, chosenRelease.TagName, colorReset, repo)

	if !autoConfirm {
		fmt.Printf("%s[WARNING]%s Xray Core will be updated and restarted.\n", colorYellow, colorReset)
		fmt.Print("Proceed with core update? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("Aborted.")
			return
		}
	}

	asset, err := updater.MatchAsset(chosenRelease.Assets, updater.GetCurrentOS(), updater.AssetArch(), "rw-core")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	fmt.Printf("Downloading %s (%.1f MB)...\n", asset.Name, float64(asset.Size)/(1024*1024))
	tmpPath, err := updater.DownloadReleaseBinary(*chosenRelease, *asset, "rw-core")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Download failed: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	fmt.Println("Validating downloaded core binary...")
	if err := updater.ValidateCoreBinary(tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Core validation failed: %v\n", colorRed, colorReset, err)
		_ = os.Remove(tmpPath)
		os.Exit(1)
	}

	targetCorePath := xray.FindCorePath(false)
	if targetCorePath == "" {
		exe, _ := os.Executable()
		targetCorePath = filepath.Join(filepath.Dir(exe), "rw-core")
	}
	fmt.Printf("Applying core update to %s...\n", targetCorePath)
	unlock, err := updater.LockTarget(targetCorePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer unlock()
	if err := updater.InstallStockCore(tmpPath, targetCorePath); err != nil {
		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Failed to replace core: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}
	assetDir := os.Getenv("XRAY_LOCATION_ASSET")
	if assetDir == "" {
		assetDir = filepath.Dir(targetCorePath)
	}
	if err := updater.EnsureGeodata(assetDir); err != nil {
		_ = updater.Rollback(targetCorePath)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("%s[SUCCESS]%s Core updated to %s!\n", colorGreen, colorReset, chosenRelease.TagName)
	fmt.Println("Restarting gowave-node to reload core...")
	d, _ := config.ReadDiscovery()
	if d.PID == 0 {
		updater.CommitUpdate(targetCorePath)
	}
	if d.PID != 0 {
		if err := CompleteUpdate(targetCorePath, Ready(true), d.PID); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
