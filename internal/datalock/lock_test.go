package datalock

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestLockContentionAndRelease(t *testing.T) {
	root := t.TempDir()
	first, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Acquire(root); !errors.Is(err, ErrBusy) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("second lock = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
}

func TestLockReleasedAfterProcessExit(t *testing.T) {
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestLockHelper$")
	child.Env = append(os.Environ(), "KAVEN_TEST_LOCK_ROOT="+root)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("helper = %q %v", line, err)
	}
	if file, err := Acquire(root); !errors.Is(err, ErrBusy) {
		if file != nil {
			file.Close()
		}
		t.Fatalf("process contention = %v", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	file, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}

func TestLockHelper(t *testing.T) {
	root := os.Getenv("KAVEN_TEST_LOCK_ROOT")
	if root == "" {
		return
	}
	file, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fmt.Println("locked")
	// Block on a pipe held open by this process until the parent terminates it.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	var buffer [1]byte
	_, _ = reader.Read(buffer[:])
}
