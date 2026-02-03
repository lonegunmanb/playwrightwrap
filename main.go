package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Logger wraps logging functionality
type Logger struct {
	enabled bool
	file    *os.File
}

// NewLogger creates a new logger, enabled if PLAYWRIGHTWRAPLOG env var is set
func NewLogger(logPath string) *Logger {
	logger := &Logger{enabled: false}
	if os.Getenv("PLAYWRIGHTWRAPLOG") != "" {
		logFile, err := os.Create(logPath)
		if err == nil {
			logger.enabled = true
			logger.file = logFile
		}
	}
	return logger
}

// Log writes a log message with timestamp if logging is enabled
func (l *Logger) Log(format string, args ...interface{}) {
	if !l.enabled || l.file == nil {
		return
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	message := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.file, "[%s] %s\n", timestamp, message)
}

// Close closes the log file
func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}

func main() {
	// Source storage state file
	storageStatePath := "./browser_profile/storage_state.json"

	// Ensure tmp directory exists
	tmpDir := "./tmp"
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create tmp directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Create a temporary file for the storage state in tmp directory
	tempFile, err := os.CreateTemp(tmpDir, "storage_state_*.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create temp file: %v\n", err)
		os.Exit(1)
	}
	tempFilePath := tempFile.Name()

	// Create logger with log file path based on temp file name
	logPath := tempFilePath + ".log"
	logger := NewLogger(logPath)
	defer logger.Close()

	logger.Log("Program started")
	logger.Log("Temp file created: %s", tempFilePath)
	logger.Log("Original args: %v", os.Args[1:])

	// Ensure temp file is cleaned up on exit
	defer os.Remove(tempFilePath)

	// Copy the storage state to the temp file
	sourceFile, err := os.Open(storageStatePath)
	if err != nil {
		logger.Log("Failed to open storage state file: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to open storage state file %s: %v\n", storageStatePath, err)
		os.Exit(1)
	}

	_, err = io.Copy(tempFile, sourceFile)
	sourceFile.Close()
	tempFile.Close()
	if err != nil {
		logger.Log("Failed to copy storage state: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to copy storage state: %v\n", err)
		os.Exit(1)
	}
	logger.Log("Storage state copied from %s to %s", storageStatePath, tempFilePath)

	// Filter out --isolated and --storage-state from arguments
	filteredArgs := filterArgs(os.Args[1:])
	logger.Log("Filtered args: %v", filteredArgs)

	// Build the command arguments
	args := []string{"@playwright/mcp", "--isolated", "--storage-state=" + tempFilePath}
	args = append(args, filteredArgs...)
	logger.Log("Final command: npx %v", args)

	// Create the command
	cmd := exec.Command("npx", args...)

	// Redirect stdin, stdout, stderr
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Handle signals to forward them to the child process
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Start the process
	if err := cmd.Start(); err != nil {
		logger.Log("Failed to start playwright: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to start playwright: %v\n", err)
		os.Exit(1)
	}
	logger.Log("Playwright process started with PID: %d", cmd.Process.Pid)

	// Forward signals to child process
	go func() {
		for sig := range sigChan {
			logger.Log("Received signal: %v, forwarding to child process", sig)
			if cmd.Process != nil {
				cmd.Process.Signal(sig)
			}
		}
	}()

	// Wait for the process to finish
	if err := cmd.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			logger.Log("Process exited with code: %d", exitError.ExitCode())
			os.Exit(exitError.ExitCode())
		}
		logger.Log("Process error: %v", err)
		fmt.Fprintf(os.Stderr, "Process error: %v\n", err)
		os.Exit(1)
	}
	logger.Log("Process finished successfully")
}

// filterArgs removes --isolated and --storage-state arguments from the slice
func filterArgs(args []string) []string {
	var result []string
	skipNext := false

	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}

		// Skip --isolated
		if arg == "--isolated" {
			continue
		}

		// Skip --storage-state=value or --storage-state value
		if arg == "--storage-state" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "--storage-state=") {
			continue
		}

		// Check if this is a combined short form or other variations
		// For safety, also handle -isolated if it exists
		if arg == "-isolated" {
			continue
		}

		_ = i // suppress unused variable warning
		result = append(result, arg)
	}

	return result
}

// getExecutableDir returns the directory where the executable is located
func getExecutableDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(executable), nil
}
