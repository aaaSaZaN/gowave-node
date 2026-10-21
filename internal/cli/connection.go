package cli

import (
	"context"
	"fmt"
	"github.com/aaaSaZaN/gowave-node/internal/config"
	"github.com/aaaSaZaN/gowave-node/internal/updater"
	"net"
	"net/http"
	"net/url"
	"time"
)

func internalClient() (client *http.Client, token string, err error) {
	d, err := config.ReadDiscovery()
	if err != nil {
		return nil, "", err
	}
	network, address, err := config.SocketAddress(d.Socket)
	if err != nil {
		return nil, "", err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
	}}
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}, url.QueryEscape(d.Token), nil
}
func Ready(core bool) bool {
	return ready(context.Background(), core)
}

func ready(ctx context.Context, core bool) bool {
	client, token, err := internalClient()
	if err != nil {
		return false
	}
	defer client.CloseIdleConnections()
	path := "logs?lines=1"
	if core {
		path = "ready?"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/internal/"+path+"&token="+token, nil)
	if err != nil {
		return false
	}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == 200
}
func WaitReady(core bool, timeout time.Duration, oldPID int) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for ctx.Err() == nil {
		d, err := config.ReadDiscovery()
		if err == nil && (oldPID == 0 || d.PID != oldPID) && ready(ctx, core) {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("node readiness timed out after %s", timeout)
}

var restartNode = updater.RestartNodeProcess
var waitForReady = WaitReady
var updateDelay = time.Second

func CompleteUpdate(target string, core bool, oldPID int) error {
	// Give an HTTP caller time to consume the successful response before restarting.
	time.Sleep(updateDelay)
	err := restartNode()
	if err == nil {
		// Xray configuration arrives from the panel after the node restarts.
		err = waitForReady(false, 120*time.Second, oldPID)
	}
	if err != nil {
		rollbackErr := updater.Rollback(target)
		if rollbackErr != nil {
			return fmt.Errorf("update failed: %v; rollback failed: %w", err, rollbackErr)
		}
		if restartErr := restartNode(); restartErr != nil {
			return fmt.Errorf("update failed: %v; rollback restart: %w", err, restartErr)
		}
		return fmt.Errorf("update failed and previous executable restored: %w", err)
	}
	updater.CommitUpdate(target)
	return nil
}
