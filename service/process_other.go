//go:build !windows

package service

func processUsage([]int) map[int]usage { return psUsage() }

func listPortsWindows(map[int]bool, map[int]string) ([]Port, error) { return nil, nil }
