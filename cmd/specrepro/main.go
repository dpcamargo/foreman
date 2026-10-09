package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dpcamargo/foreman/internal/adapters/sandbox"
)

func main() {
	ctx := context.Background()
	ws := sandbox.New(os.Getenv("HOME") + "/.foreman/work")
	repo := os.Getenv("HOME") + "/.foreman/work/fixtures/addrepo.git"
	base := "9c445d999adf03d0822e9a1e25dabcd93a3a1a6f"
	dir, err := ws.PrepareWorkspace(ctx, "tsk_repro", "run_repro", repo, base)
	fmt.Println("dir:", dir)
	fmt.Println("err:", err)
}
