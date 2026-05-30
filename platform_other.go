//go:build !windows

package main

func stopVSCode() (string, error)             { return "", nil }
func startVSCode(_ string) error              { return nil }
func windowsAnthropicDirs() []string          { return nil }
func updateWindowsCredentialManager(_ string) {}
func stopCodex() (string, error)              { return "", nil }
func startCodex(_ string) error               { return nil }
