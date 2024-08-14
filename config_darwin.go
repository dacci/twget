package main

import (
	"os"
	"path"
)

func GetConfigDir() string {
	return path.Join(os.Getenv("HOME"), "Library", "Application Support")
}
