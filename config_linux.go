package main

import (
	"os"
	"path"
)

func GetConfigDir() string {
	home, ok := os.LookupEnv("XDG_CONFIG_HOME")
	if !ok {
		home = os.Getenv("HOME")
	}
	return path.Join(home, ".config")
}
