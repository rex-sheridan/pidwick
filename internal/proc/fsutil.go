package proc

import "os"

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }
