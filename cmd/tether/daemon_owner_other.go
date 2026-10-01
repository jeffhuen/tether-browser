//go:build !linux && !darwin

package main

func localDaemonOwner(string) (int, bool) { return 0, false }
func runningDaemonVersion(int) string     { return "" }
