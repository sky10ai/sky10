package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	restore, err := isolateTestHome("sky10-sandbox-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolate test home: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}

func isolateTestHome(prefix string) (func(), error) {
	home, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, err
	}

	oldHome, hadHome := os.LookupEnv("HOME")
	oldUserProfile, hadUserProfile := os.LookupEnv("USERPROFILE")
	oldHomeDrive, hadHomeDrive := os.LookupEnv("HOMEDRIVE")
	oldHomePath, hadHomePath := os.LookupEnv("HOMEPATH")

	if err := os.Setenv("HOME", home); err != nil {
		return nil, err
	}
	if err := os.Setenv("USERPROFILE", home); err != nil {
		return nil, err
	}

	volume := filepath.VolumeName(home)
	if volume != "" {
		if err := os.Setenv("HOMEDRIVE", volume); err != nil {
			return nil, err
		}
	}
	homePath := home[len(volume):]
	if homePath != "" {
		if err := os.Setenv("HOMEPATH", homePath); err != nil {
			return nil, err
		}
	}

	return func() {
		if hadHome {
			_ = os.Setenv("HOME", oldHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadUserProfile {
			_ = os.Setenv("USERPROFILE", oldUserProfile)
		} else {
			_ = os.Unsetenv("USERPROFILE")
		}
		if hadHomeDrive {
			_ = os.Setenv("HOMEDRIVE", oldHomeDrive)
		} else {
			_ = os.Unsetenv("HOMEDRIVE")
		}
		if hadHomePath {
			_ = os.Setenv("HOMEPATH", oldHomePath)
		} else {
			_ = os.Unsetenv("HOMEPATH")
		}
		_ = os.RemoveAll(home)
	}, nil
}
