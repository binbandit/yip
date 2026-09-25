package main

import "errors"

var errPending = errors.New("not yet implemented in this build")

func runDoctor(args []string) error  { return errPending }
func runBackup(args []string) error  { return errPending }
func runRestore(args []string) error { return errPending }
func runOwner(args []string) error   { return errPending }
func runForge(args []string) error   { return errPending }
func runService(args []string) error { return errPending }
