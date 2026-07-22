//go:build !windows

package main

func stopCodex() (string, error) { return "", nil }
func startCodex(_ string) error  { return nil }
