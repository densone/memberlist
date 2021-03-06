package memberlist

import (
	"bytes"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
)

var bindLock sync.Mutex
var bindNum byte = 10

func getBindAddr() net.IP {
	bindLock.Lock()
	defer bindLock.Unlock()

	result := net.IPv4(127, 0, 0, bindNum)
	bindNum++
	if bindNum > 255 {
		bindNum = 10
	}

	return result
}

func testConfig() *Config {
	config := DefaultLANConfig()
	config.BindAddr = getBindAddr().String()
	config.Name = config.BindAddr
	return config
}

func yield() {
	time.Sleep(5 * time.Millisecond)
}

type MockDelegate struct {
	meta        []byte
	msgs        [][]byte
	broadcasts  [][]byte
	state       []byte
	remoteState []byte
}

func (m *MockDelegate) NodeMeta(limit int) []byte {
	return m.meta
}

func (m *MockDelegate) NotifyMsg(msg []byte) {
	m.msgs = append(m.msgs, msg)
}

func (m *MockDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	b := m.broadcasts
	m.broadcasts = nil
	return b
}

func (m *MockDelegate) LocalState(join bool) []byte {
	return m.state
}

func (m *MockDelegate) MergeRemoteState(s []byte, join bool) {
	m.remoteState = s
}

func GetMemberlistDelegate(t *testing.T) (*Memberlist, *MockDelegate) {
	d := &MockDelegate{}

	c := testConfig()
	c.Delegate = d

	var m *Memberlist
	var err error
	for i := 0; i < 100; i++ {
		m, err = newMemberlist(c)
		if err == nil {
			return m, d
		}
		c.BindPort++
	}
	t.Fatalf("failed to start: %v", err)
	return nil, nil
}

func GetMemberlist(t *testing.T) *Memberlist {
	c := testConfig()

	var m *Memberlist
	var err error
	for i := 0; i < 100; i++ {
		m, err = newMemberlist(c)
		if err == nil {
			return m
		}
		c.BindPort++
	}
	t.Fatalf("failed to start: %v", err)
	return nil
}

func TestDefaultLANConfig_protocolVersion(t *testing.T) {
	c := DefaultLANConfig()
	if c.ProtocolVersion != ProtocolVersionMax {
		t.Fatalf("should be max: %d", c.ProtocolVersion)
	}
}

func TestCreate_protocolVersion(t *testing.T) {
	cases := []struct {
		version uint8
		err     bool
	}{
		{ProtocolVersionMin, false},
		{ProtocolVersionMax, false},
		// TODO(mitchellh): uncommon when we're over 0
		//{ProtocolVersionMin - 1, true},
		{ProtocolVersionMax + 1, true},
		{ProtocolVersionMax - 1, false},
	}

	for _, tc := range cases {
		c := DefaultLANConfig()
		c.BindAddr = getBindAddr().String()
		c.ProtocolVersion = tc.version
		m, err := Create(c)
		if tc.err && err == nil {
			t.Errorf("Should've failed with version: %d", tc.version)
		} else if !tc.err && err != nil {
			t.Errorf("Version '%d' error: %s", tc.version, err)
		}

		if err == nil {
			m.Shutdown()
		}
	}
}

func TestCreate_secretKey(t *testing.T) {
	cases := []struct {
		key []byte
		err bool
	}{
		{make([]byte, 0), false},
		{[]byte("abc"), true},
		{make([]byte, 16), false},
	}

	for _, tc := range cases {
		c := DefaultLANConfig()
		c.BindAddr = getBindAddr().String()
		c.SecretKey = tc.key
		m, err := Create(c)
		if tc.err && err == nil {
			t.Errorf("Should've failed with key: %#v", tc.key)
		} else if !tc.err && err != nil {
			t.Errorf("Key '%#v' error: %s", tc.key, err)
		}

		if err == nil {
			m.Shutdown()
		}
	}
}

func TestCreate_secretKeyEmpty(t *testing.T) {
	c := DefaultLANConfig()
	c.BindAddr = getBindAddr().String()
	c.SecretKey = make([]byte, 0)
	m, err := Create(c)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m.Shutdown()

	if m.config.SecretKey != nil {
		t.Fatalf("bad: %#v", m.config.SecretKey)
	}
}

func TestCreate(t *testing.T) {
	c := testConfig()
	c.ProtocolVersion = ProtocolVersionMin
	c.DelegateProtocolVersion = 13
	c.DelegateProtocolMin = 12
	c.DelegateProtocolMax = 24

	m, err := Create(c)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m.Shutdown()

	yield()

	members := m.Members()
	if len(members) != 1 {
		t.Fatalf("bad number of members")
	}

	if members[0].PMin != ProtocolVersionMin {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].PMax != ProtocolVersionMax {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].PCur != c.ProtocolVersion {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].DMin != c.DelegateProtocolMin {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].DMax != c.DelegateProtocolMax {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].DCur != c.DelegateProtocolVersion {
		t.Fatalf("bad: %#v", members[0])
	}
}

func TestMemberList_CreateShutdown(t *testing.T) {
	m := GetMemberlist(t)
	m.schedule()
	if err := m.Shutdown(); err != nil {
		t.Fatalf("failed to shutdown %v", err)
	}
}

func TestMemberList_Members(t *testing.T) {
	n1 := &Node{Name: "test"}
	n2 := &Node{Name: "test2"}
	n3 := &Node{Name: "test3"}

	m := &Memberlist{}
	nodes := []*nodeState{
		&nodeState{Node: *n1, State: stateAlive},
		&nodeState{Node: *n2, State: stateDead},
		&nodeState{Node: *n3, State: stateSuspect},
	}
	m.nodes = nodes

	members := m.Members()
	if !reflect.DeepEqual(members, []*Node{n1, n3}) {
		t.Fatalf("bad members")
	}
}

func TestMemberlist_Join(t *testing.T) {
	m1 := GetMemberlist(t)
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	// Create a second node
	c := DefaultLANConfig()
	addr1 := getBindAddr()
	c.Name = addr1.String()
	c.BindAddr = addr1.String()
	c.BindPort = m1.config.BindPort

	m2, err := Create(c)
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	defer m2.Shutdown()

	num, err := m2.Join([]string{m1.config.BindAddr})
	if num != 1 {
		t.Fatal("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
}

func TestMemberlist_Join_protocolVersions(t *testing.T) {
	c1 := testConfig()
	c2 := testConfig()
	c3 := testConfig()
	c3.ProtocolVersion = ProtocolVersionMax

	m1, err := Create(c1)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m1.Shutdown()

	m2, err := Create(c2)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m2.Shutdown()

	m3, err := Create(c3)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m3.Shutdown()

	_, err = m1.Join([]string{c2.BindAddr})
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	yield()

	_, err = m1.Join([]string{c3.BindAddr})
	if err != nil {
		t.Fatalf("err: %s", err)
	}
}

func TestMemberlist_Leave(t *testing.T) {
	m1 := GetMemberlist(t)
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	// Create a second node
	c := DefaultLANConfig()
	addr1 := getBindAddr()
	c.Name = addr1.String()
	c.BindAddr = addr1.String()
	c.BindPort = m1.config.BindPort
	c.GossipInterval = time.Millisecond

	m2, err := Create(c)
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	defer m2.Shutdown()

	num, err := m2.Join([]string{m1.config.BindAddr})
	if num != 1 {
		t.Fatal("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
	if len(m1.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	// Leave
	m1.Leave(time.Second)

	// Wait for leave
	time.Sleep(10 * time.Millisecond)

	// m1 should think dead
	if len(m1.Members()) != 1 {
		t.Fatalf("should have 1 node")
	}

	if len(m2.Members()) != 1 {
		t.Fatalf("should have 1 node")
	}
}

func TestMemberlist_JoinShutdown(t *testing.T) {
	m1 := GetMemberlist(t)
	m1.setAlive()
	m1.schedule()

	// Create a second node
	c := DefaultLANConfig()
	addr1 := getBindAddr()
	c.Name = addr1.String()
	c.BindAddr = addr1.String()
	c.BindPort = m1.config.BindPort
	c.ProbeInterval = time.Millisecond
	c.ProbeTimeout = 100 * time.Microsecond

	m2, err := Create(c)
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	defer m2.Shutdown()

	num, err := m2.Join([]string{m1.config.BindAddr})
	if num != 1 {
		t.Fatal("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	m1.Shutdown()

	time.Sleep(10 * time.Millisecond)

	if len(m2.Members()) != 1 {
		t.Fatalf("should have 1 nodes! %v", m2.Members())
	}
}

func TestMemberlist_delegateMeta(t *testing.T) {
	c1 := testConfig()
	c2 := testConfig()
	c1.Delegate = &MockDelegate{meta: []byte("web")}
	c2.Delegate = &MockDelegate{meta: []byte("lb")}

	m1, err := Create(c1)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m1.Shutdown()

	m2, err := Create(c2)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m2.Shutdown()

	_, err = m1.Join([]string{c2.BindAddr})
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	yield()

	var roles map[string]string

	// Check the roles of members of m1
	m1m := m1.Members()
	if len(m1m) != 2 {
		t.Fatalf("bad: %#v", m1m)
	}

	roles = make(map[string]string)
	for _, m := range m1m {
		roles[m.Name] = string(m.Meta)
	}

	if r := roles[c1.Name]; r != "web" {
		t.Fatalf("bad role for %s: %s", c1.Name, r)
	}

	if r := roles[c2.Name]; r != "lb" {
		t.Fatalf("bad role for %s: %s", c2.Name, r)
	}

	// Check the roles of members of m2
	m2m := m2.Members()
	if len(m2m) != 2 {
		t.Fatalf("bad: %#v", m2m)
	}

	roles = make(map[string]string)
	for _, m := range m2m {
		roles[m.Name] = string(m.Meta)
	}

	if r := roles[c1.Name]; r != "web" {
		t.Fatalf("bad role for %s: %s", c1.Name, r)
	}

	if r := roles[c2.Name]; r != "lb" {
		t.Fatalf("bad role for %s: %s", c2.Name, r)
	}
}

func TestMemberlist_UserData(t *testing.T) {
	m1, d1 := GetMemberlistDelegate(t)
	d1.state = []byte("something")
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	// Create a second delegate with things to send
	d2 := &MockDelegate{}
	d2.broadcasts = [][]byte{
		[]byte("test"),
		[]byte("foobar"),
	}
	d2.state = []byte("my state")

	// Create a second node
	c := DefaultLANConfig()
	addr1 := getBindAddr()
	c.Name = addr1.String()
	c.BindAddr = addr1.String()
	c.BindPort = m1.config.BindPort
	c.GossipInterval = time.Millisecond
	c.PushPullInterval = time.Millisecond
	c.Delegate = d2

	m2, err := Create(c)
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	num, err := m2.Join([]string{m1.config.BindAddr})
	if num != 1 {
		t.Fatal("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	defer m2.Shutdown()

	// Check the hosts
	if m2.NumMembers() != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	// Wait for a little while
	time.Sleep(3 * time.Millisecond)

	// Ensure we got the messages
	if len(d1.msgs) != 2 {
		t.Fatalf("should have 2 messages!")
	}
	if !reflect.DeepEqual(d1.msgs[0], []byte("test")) {
		t.Fatalf("bad msg %v", d1.msgs[0])
	}
	if !reflect.DeepEqual(d1.msgs[1], []byte("foobar")) {
		t.Fatalf("bad msg %v", d1.msgs[1])
	}

	// Check the push/pull state
	if !reflect.DeepEqual(d1.remoteState, []byte("my state")) {
		t.Fatalf("bad state %s", d1.remoteState)
	}
	if !reflect.DeepEqual(d2.remoteState, []byte("something")) {
		t.Fatalf("bad state %s", d2.remoteState)
	}
}

func TestMemberlistProtocolVersion(t *testing.T) {
	c := DefaultLANConfig()
	c.BindAddr = getBindAddr().String()
	c.ProtocolVersion = ProtocolVersionMax
	m, err := Create(c)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m.Shutdown()

	result := m.ProtocolVersion()
	if result != ProtocolVersionMax {
		t.Fatalf("bad: %d", result)
	}
}

func TestMemberlist_Join_DeadNode(t *testing.T) {
	m1 := GetMemberlist(t)
	m1.config.TCPTimeout = 50 * time.Millisecond
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	// Create a second "node", which is just a TCP listener that
	// does not ever respond. This is to test our deadliens
	addr1 := getBindAddr()
	list, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr1.String(), m1.config.BindPort))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer list.Close()

	// Ensure we don't hang forever
	timer := time.AfterFunc(100*time.Millisecond, func() {
		panic("should have timed out by now")
	})
	defer timer.Stop()

	num, err := m1.Join([]string{addr1.String()})
	if num != 0 {
		t.Fatal("unexpected 0: %d", num)
	}
	if err == nil {
		t.Fatal("expect err")
	}
}

func TestMemberlist_Join_Proto1And2(t *testing.T) {
	// Create first node, protocol 2
	m1 := GetMemberlist(t)
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()
	if m1.config.ProtocolVersion != 2 {
		t.Fatalf("expected version 2")
	}

	// Create a second node, lower protocol!
	c := DefaultLANConfig()
	addr1 := getBindAddr()
	c.Name = addr1.String()
	c.BindAddr = addr1.String()
	c.BindPort = m1.config.BindPort
	c.ProtocolVersion = 1

	m2, err := Create(c)
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	defer m2.Shutdown()

	num, err := m2.Join([]string{m1.config.BindAddr})
	if num != 1 {
		t.Fatal("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	// Check the hosts
	if len(m1.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
}

func TestMemberlist_Join_IPv6(t *testing.T) {
	c1 := DefaultLANConfig()
	c1.Name = "A"
	c1.BindAddr = "[::1]"
	var m1 *Memberlist
	var err error
	for i := 0; i < 100; i++ {
		c1.BindPort = 23456 + i
		m1, err = Create(c1)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	defer m1.Shutdown()

	// Create a second node
	c2 := DefaultLANConfig()
	c2.Name = "B"
	c2.BindAddr = "[::1]"
	var m2 *Memberlist
	for i := 0; i < 100; i++ {
		c2.BindPort = c1.BindPort + 1 + i
		m2, err = Create(c2)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}
	defer m2.Shutdown()

	num, err := m2.Join([]string{fmt.Sprintf("%s:%d", m1.config.BindAddr, 23456)})
	if num != 1 {
		t.Fatal("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	if len(m1.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
}

func TestAdvertiseAddr(t *testing.T) {
	c := testConfig()
	c.AdvertiseAddr = "127.0.1.100"
	c.AdvertisePort = 23456

	m, err := Create(c)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer m.Shutdown()

	yield()

	members := m.Members()
	if len(members) != 1 {
		t.Fatalf("bad number of members")
	}

	if bytes.Compare(members[0].Addr, []byte{127, 0, 1, 100}) != 0 {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].Port != 23456 {
		t.Fatalf("bad: %#v", members[0])
	}
}
