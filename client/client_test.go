package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type TestServer struct {
	cmd      *exec.Cmd
	port     int
	identity string
	stateDir string
}

func NewTestServer(t *testing.T) (*TestServer, error) {
	var err error
	server := new(TestServer)
	if server.stateDir, err = ioutil.TempDir("", "pond-client-test"); err != nil {
		return nil, err
	}
	server.cmd = exec.Command("../server/server",
		"--init",
		"--base-directory", server.stateDir,
		"--port", "0",
	)
	rawStderr, err := server.cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := server.cmd.Start(); err != nil {
		return nil, err
	}

	stderr := bufio.NewReader(rawStderr)

	for {
		line, isPrefix, err := stderr.ReadLine()
		if err != nil {
			return nil, errors.New("error while reading status line: " + err.Error())
		}
		if isPrefix {
			continue
		}
		if i := bytes.Index(line, []byte("Started. Listening on port ")); i != -1 {
			line = line[i:]
			words := strings.Split(string(line), " ")
			if len(words) < 8 {
				return nil, errors.New("status line from server has unexpected form: " + string(line))
			}
			portStr := words[4]
			server.identity = words[7]

			if server.port, err = strconv.Atoi(portStr); err != nil {
				return nil, errors.New("failed to parse port number: " + err.Error())
			}
			break
		}
	}

	go func() {
		for {
			line, _, err := stderr.ReadLine()
			if err != nil {
				return
			}
			t.Logf("%s\n", string(line))
		}
	}()

	return server, nil
}

func (server *TestServer) URL() string {
	return fmt.Sprintf("pondserver://%s@127.0.0.1:%d", server.identity, server.port)
}

func (server *TestServer) Close() {
	server.cmd.Process.Kill()
	server.cmd.Wait()
	os.RemoveAll(server.stateDir)
}

type TestUI struct {
	actions        chan interface{}
	events         chan interface{}
	signal         chan bool
	currentStateID int
	t              *testing.T
	text           map[string]string
}

func NewTestUI(t *testing.T) *TestUI {
	return &TestUI{
		actions:        make(chan interface{}, 16),
		events:         make(chan interface{}, 16),
		signal:         make(chan bool),
		currentStateID: uiStateInvalid,
		t:              t,
		text:           make(map[string]string),
	}
}

func (ui *TestUI) Actions() chan<- interface{} {
	return ui.actions
}

func (ui *TestUI) Events() <-chan interface{} {
	return ui.events
}

func (ui *TestUI) Signal() {
	ui.signal <- true
}

func (ui *TestUI) Run() {
	panic("should never be called")
}

func (ui *TestUI) WaitForSignal() error {
	var uierr error
	<-ui.signal

ReadActions:
	for {
		select {
		case action := <-ui.actions:
			ui.t.Logf("%#v", action)
			switch action := action.(type) {
			case UIState:
				ui.currentStateID = action.stateID
			case UIError:
				uierr = action.err
			case SetText:
				ui.text[action.name] = action.text
			case SetTextView:
				ui.text[action.name] = action.text
			}
		default:
			break ReadActions
		}
	}

	return uierr
}

type TestClient struct {
	*client
	stateDir string
	ui       *TestUI
}

func NewTestClient(t *testing.T) (*TestClient, error) {
	tc := &TestClient{
		ui: NewTestUI(t),
	}
	var err error
	if tc.stateDir, err = ioutil.TempDir("", "pond-client-test"); err != nil {
		return nil, err
	}
	tc.client = NewClient(filepath.Join(tc.stateDir, "state"), tc.ui, rand.Reader, true)
	return tc, nil
}

func (tc *TestClient) Close() {
	tc.Shutdown()
	os.RemoveAll(tc.stateDir)
}

func (tc *TestClient) AdvanceTo(state int) {
	tc.ui.currentStateID = uiStateInvalid
	for tc.ui.currentStateID != state {
		if err := tc.ui.WaitForSignal(); err != nil {
			tc.ui.t.Fatal(err)
		}
	}
}

func TestAccountCreation(t *testing.T) {
	t.Parallel()

	server, err := NewTestServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	client, err := NewTestClient(t)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	client.ui.WaitForSignal()
	if id := client.ui.currentStateID; id != uiStateLoading {
		t.Fatalf("client in UI state %d when it was expected to be loading", id)
	}

	client.ui.WaitForSignal()
	if id := client.ui.currentStateID; id != uiStateCreatePassphrase {
		t.Fatalf("client in UI state %d when it was expected to be creating a passphrase", id)
	}
	client.ui.events <- Click{
		name:    "next",
		entries: map[string]string{"pw": ""},
	}

	client.ui.WaitForSignal()
	if id := client.ui.currentStateID; id != uiStateCreateAccount {
		t.Fatalf("client in UI state %d when it was expected to be creating an account", id)
	}

	client.ui.events <- Click{
		name:    "create",
		entries: map[string]string{"server": "asldfjksadfkl"},
	}
	t.Log("Waiting for error from garbage URL")
	for {
		if err := client.ui.WaitForSignal(); err != nil {
			break
		}
	}

	url := server.URL()
	client.ui.events <- Click{
		name:    "create",
		entries: map[string]string{"server": url[:len(url)-1]},
	}

	t.Log("Waiting for error from invalid port")
	for {
		if err := client.ui.WaitForSignal(); err != nil {
			break
		}
	}

	t.Log("Waiting for success")
	client.ui.events <- Click{
		name:    "create",
		entries: map[string]string{"server": url},
	}
	client.AdvanceTo(uiStateMain)
}

func proceedToMainUI(t *testing.T, client *TestClient, server *TestServer) {
	client.AdvanceTo(uiStateCreatePassphrase)
	client.ui.events <- Click{
		name:    "next",
		entries: map[string]string{"pw": ""},
	}
	client.AdvanceTo(uiStateCreateAccount)
	url := server.URL()
	client.ui.events <- Click{
		name:    "create",
		entries: map[string]string{"server": url},
	}
	client.AdvanceTo(uiStateMain)
}

func proceedToKeyExchange(t *testing.T, client *TestClient, server *TestServer, otherName string) {
	proceedToMainUI(t, client, server)

	client.ui.events <- Click{name: "newcontact"}
	client.AdvanceTo(uiStateNewContact)

	client.ui.events <- Click{
		name:    "create",
		entries: map[string]string{"name": otherName},
	}
	client.AdvanceTo(uiStateNewContact2)
}

func proceedToPaired(t *testing.T, client1, client2 *TestClient, server *TestServer) {
	proceedToKeyExchange(t, client1, server, "client2")
	proceedToKeyExchange(t, client2, server, "client1")

	client1.ui.events <- Click{
		name:      "process",
		textViews: map[string]string{"kxin": client2.ui.text["kxout"]},
	}
	client1.AdvanceTo(uiStateShowContact)

	client2.ui.events <- Click{
		name:      "process",
		textViews: map[string]string{"kxin": client1.ui.text["kxout"]},
	}
	client2.AdvanceTo(uiStateShowContact)
}

func TestKeyExchange(t *testing.T) {
	t.Parallel()

	server, err := NewTestServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	client1, err := NewTestClient(t)
	if err != nil {
		t.Fatal(err)
	}
	defer client1.Close()

	client2, err := NewTestClient(t)
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close()

	proceedToKeyExchange(t, client1, server, "client2")
	proceedToKeyExchange(t, client2, server, "client1")

	client1.ui.events <- Click{
		name:      "process",
		textViews: map[string]string{"kxin": "rubbish"},
	}
	t.Log("Waiting for error from garbage key exchange")
	for {
		if err := client1.ui.WaitForSignal(); err != nil {
			break
		}
	}

	kxBytes := []byte(client2.ui.text["kxout"])
	kxBytes[55] ^= 1
	client1.ui.events <- Click{
		name:      "process",
		textViews: map[string]string{"kxin": string(kxBytes)},
	}
	t.Log("Waiting for error from corrupt key exchange")
	for {
		if err := client1.ui.WaitForSignal(); err != nil {
			break
		}
	}
	client1.ui.events <- Click{
		name:      "process",
		textViews: map[string]string{"kxin": client2.ui.text["kxout"]},
	}
	client1.AdvanceTo(uiStateShowContact)

	client2.ui.events <- Click{
		name:      "process",
		textViews: map[string]string{"kxin": client1.ui.text["kxout"]},
	}
	client2.AdvanceTo(uiStateShowContact)
}

func contactByName(client *TestClient, name string) (id uint64, contact *Contact) {
	for id, contact = range client.contacts {
		if contact.name == name {
			return
		}
	}
	panic("contact not found: " + name)
}

func selectContact(t *testing.T, client *TestClient, name string) {
	id, _ := contactByName(client, name)
	var boxName string
	for _, item := range client.contactsUI.entries {
		if item.id == id {
			boxName = item.boxName
		}
	}
	if len(boxName) == 0 {
		panic("couldn't find box for given name")
	}
	client.ui.events <- Click{name: boxName}
	client.AdvanceTo(uiStateShowContact)
}

func sendMessage(client *TestClient, to string, message string) {
	client.ui.events <- Click{name: "compose"}
	client.AdvanceTo(uiStateCompose)

	client.ui.events <- Click{
		name:      "send",
		combos:    map[string]string{"to": to},
		textViews: map[string]string{"body": message},
	}

	client.AdvanceTo(uiStateOutbox)
	ackChan := make(chan bool)
	client.fetchNowChan <- ackChan
	<-ackChan
}

func fetchMessage(client *TestClient) (from, message string) {
	ackChan := make(chan bool)
	client.fetchNowChan <- ackChan

WaitForAck:
	for {
		select {
		case <-client.ui.signal:
			break
		case <-ackChan:
			break WaitForAck
		}
	}

	if len(client.inbox) == 0 {
		panic("no messages")
	}
	m := client.inbox[len(client.inbox)-1]
	from = client.contacts[m.from].name
	message = string(m.message.Body)
	return
}

func TestMessageExchange(t *testing.T) {
	t.Parallel()

	server, err := NewTestServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	client1, err := NewTestClient(t)
	if err != nil {
		t.Fatal(err)
	}
	defer client1.Close()

	client2, err := NewTestClient(t)
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close()

	proceedToPaired(t, client1, client2, server)

	for i := 0; i < 3; i++ {
		testMsg := fmt.Sprintf("test message %d", i)
		sendMessage(client1, "client2", testMsg)
		from, msg := fetchMessage(client2)
		if from != "client1" {
			t.Fatalf("message from %s, expected client1", from)
		}
		if msg != testMsg {
			t.Fatalf("Incorrect message contents: %s", msg)
		}

		sendMessage(client2, "client1", testMsg)
		from, msg = fetchMessage(client1)
		if from != "client2" {
			t.Fatalf("message from %s, expected client2", from)
		}
		if msg != testMsg {
			t.Fatalf("Incorrect message contents: %s", msg)
		}
	}
}