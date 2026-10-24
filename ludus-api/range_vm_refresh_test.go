package ludusapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"ludusapi/models"

	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
)

type fakeVM struct {
	id      int
	name    string
	running bool
	ip      string // "" makes the guest agent fail
}

// fakeProxmox serves the Proxmox API calls a range refresh makes and counts
// them by path. poolGate, when set, holds pool requests until it is closed.
type fakeProxmox struct {
	t          *testing.T
	pools      map[string][]fakeVM
	agentDelay time.Duration
	poolGate   chan struct{}

	mu    sync.Mutex
	calls map[string]int
}

var (
	fakePoolPath  = regexp.MustCompile(`^/api2/json/pools/([^/]+)$`)
	fakeQemuPath  = regexp.MustCompile(`^/api2/json/nodes/pve/qemu/(\d+)/(status/current|config|agent/network-get-interfaces)$`)
	fakeNodeStats = "/api2/json/nodes/pve/status"
)

func (f *fakeProxmox) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *fakeProxmox) vm(id int) (fakeVM, bool) {
	for _, vms := range f.pools {
		for _, vm := range vms {
			if vm.id == id {
				return vm, true
			}
		}
	}
	return fakeVM{}, false
}

func (f *fakeProxmox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls[r.URL.Path]++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api2/json/version" {
		fmt.Fprint(w, `{"data":{"version":"9.0"}}`)
		return
	}
	if r.URL.Path == fakeNodeStats {
		fmt.Fprint(w, `{"data":{}}`)
		return
	}
	if match := fakePoolPath.FindStringSubmatch(r.URL.Path); match != nil {
		if f.poolGate != nil {
			<-f.poolGate
		}
		members := ""
		for i, vm := range f.pools[match[1]] {
			if i > 0 {
				members += ","
			}
			members += fmt.Sprintf(`{"vmid":%d,"name":%q,"node":"pve","type":"qemu","status":%q}`, vm.id, vm.name, status(vm))
		}
		fmt.Fprintf(w, `{"data":{"members":[%s]}}`, members)
		return
	}
	if match := fakeQemuPath.FindStringSubmatch(r.URL.Path); match != nil {
		id, _ := strconv.Atoi(match[1])
		vm, ok := f.vm(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch match[2] {
		case "status/current":
			fmt.Fprintf(w, `{"data":{"vmid":%d,"name":%q,"status":%q}}`, vm.id, vm.name, status(vm))
		case "config":
			fmt.Fprintf(w, `{"data":{"name":%q}}`, vm.name)
		default:
			time.Sleep(f.agentDelay)
			if vm.ip == "" {
				http.Error(w, `{"data":null,"message":"QEMU guest agent is not running"}`, http.StatusInternalServerError)
				return
			}
			fmt.Fprintf(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address":%q,"ip-address-type":"ipv4"}]}]}}`, vm.ip)
		}
		return
	}
	f.t.Errorf("unexpected Proxmox request: %s", r.URL)
	http.NotFound(w, r)
}

func status(vm fakeVM) string {
	if vm.running {
		return "running"
	}
	return "stopped"
}

type refreshFixture struct {
	*hostFixture
	proxmox *fakeProxmox
	client  *goproxmox.Client
	clock   time.Time
}

func newRefreshFixture(t *testing.T, pools map[string][]fakeVM) *refreshFixture {
	f := &refreshFixture{hostFixture: newHostFixture(t), clock: time.Unix(1_000_000, 0)}
	f.proxmox = &fakeProxmox{t: t, pools: pools, calls: map[string]int{}}
	api := httptest.NewServer(f.proxmox)
	t.Cleanup(api.Close)

	previousConfiguration, previousServer, previousCache := ServerConfiguration, server, guestAgentIPs
	resetRootPVEClient := func() {
		rootPVEClientMu.Lock()
		defer rootPVEClientMu.Unlock()
		if rootPVEClient != nil {
			rootPVEClient.Close()
			rootPVEClient = nil
		}
	}
	resetRootPVEClient()
	t.Cleanup(func() {
		resetRootPVEClient()
		ServerConfiguration, server, guestAgentIPs = previousConfiguration, previousServer, previousCache
	})
	ServerConfiguration.ProxmoxEndpoints = []string{api.URL}
	ServerConfiguration.ProxmoxTokenID = "root@pam!test"
	ServerConfiguration.ProxmoxTokenSecret = "secret"
	server = f.server
	guestAgentIPs = newGuestAgentIPCache(func() time.Time { return f.clock })
	f.client = goproxmox.NewClient(api.URL + "/api2/json")
	return f
}

// refresh runs updateRangeVMData for the range with the given number in a
// new request, as an API handler would.
func (f *refreshFixture) refresh(t *testing.T, e *core.RequestEvent, number int) *models.Range {
	t.Helper()
	if e == nil {
		e = f.event(t, f.owner, f.owner, 0)
	}
	usersRange := &models.Range{}
	usersRange.SetProxyRecord(f.ranges[number])
	if err := updateRangeVMData(e, usersRange, f.client); err != nil {
		t.Fatalf("updateRangeVMData(range %d): %v", number, err)
	}
	return usersRange
}

func (f *refreshFixture) ip(t *testing.T, number, proxmoxID int) string {
	t.Helper()
	records, err := f.pb.FindAllRecords("vms")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.GetString("range") == f.ranges[number].Id && record.GetInt("proxmoxID") == proxmoxID {
			return record.GetString("ip")
		}
	}
	t.Fatalf("range %d has no VM %d", number, proxmoxID)
	return ""
}

func agentPath(id int) string {
	return fmt.Sprintf("/api2/json/nodes/pve/qemu/%d/agent/network-get-interfaces", id)
}

// Requests that arrive while a range refresh is running wait for it instead
// of querying Proxmox again, and each gets the refreshed VM count and pool.
func TestRangeRefreshIsSharedByConcurrentRequests(t *testing.T) {
	f := newRefreshFixture(t, map[string][]fakeVM{"OWNER": {
		{id: 201, name: "OWNER-a", running: true, ip: "10.10.10.1"},
		{id: 202, name: "OWNER-b", running: true, ip: "10.10.10.2"},
	}})
	f.proxmox.poolGate = make(chan struct{})

	const requests = 6
	events := make([]*core.RequestEvent, requests)
	ranges := make([]*models.Range, requests)
	var wg sync.WaitGroup
	start := func(i int) {
		events[i] = f.event(t, f.owner, f.owner, 0)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ranges[i] = f.refresh(t, events[i], 10)
		}()
	}
	start(0)
	for f.proxmox.count("/api2/json/pools/OWNER") == 0 {
		time.Sleep(time.Millisecond)
	}
	for i := 1; i < requests; i++ {
		start(i)
	}
	time.Sleep(100 * time.Millisecond) // let the others join the running refresh
	close(f.proxmox.poolGate)
	wg.Wait()

	if n := f.proxmox.count("/api2/json/pools/OWNER"); n != 1 {
		t.Fatalf("%d concurrent requests read the pool %d times, want 1", requests, n)
	}
	for i := range requests {
		pool, _ := events[i].Get("getVMsForPool_OWNER").([]goproxmox.ClusterResource)
		if ranges[i].NumberOfVms() != 2 || len(pool) != 2 {
			t.Errorf("request %d: numberOfVMs %d, pool %d VMs; want 2 and 2", i, ranges[i].NumberOfVms(), len(pool))
		}
	}
}

// One request refreshes every range it asks for once: ListRanges refreshes
// all ranges with the same request.
func TestRangeRefreshRunsOncePerRangePerRequest(t *testing.T) {
	f := newRefreshFixture(t, map[string][]fakeVM{
		"OWNER":        {{id: 201, name: "OWNER-a", running: true, ip: "10.10.10.1"}},
		"MEMBER-RANGE": {{id: 211, name: "MEMBER-RANGE-a", running: true, ip: "10.11.10.1"}},
	})
	e := f.event(t, f.owner, f.owner, 0)
	f.refresh(t, e, 10)
	f.refresh(t, e, 11)
	f.refresh(t, e, 10)
	if owner, member := f.proxmox.count("/api2/json/pools/OWNER"), f.proxmox.count("/api2/json/pools/MEMBER-RANGE"); owner != 1 || member != 1 {
		t.Fatalf("pool reads: OWNER %d, MEMBER-RANGE %d; want 1 each", owner, member)
	}
	if got := f.ip(t, 11, 211); got != "10.11.10.1" {
		t.Fatalf("second range IP = %q, want it refreshed", got)
	}
}

// Guest agents are queried in parallel, so slow agents do not add up.
func TestRangeRefreshQueriesGuestAgentsInParallel(t *testing.T) {
	vms := make([]fakeVM, 4)
	for i := range vms {
		vms[i] = fakeVM{id: 201 + i, name: fmt.Sprintf("OWNER-%d", i), running: true, ip: fmt.Sprintf("10.10.10.%d", i+1)}
	}
	f := newRefreshFixture(t, map[string][]fakeVM{"OWNER": vms})
	f.proxmox.agentDelay = 400 * time.Millisecond

	started := time.Now()
	f.refresh(t, nil, 10)
	// One at a time would take at least 4 x 400ms.
	if elapsed := time.Since(started); elapsed > 1200*time.Millisecond {
		t.Fatalf("refresh took %v with four 400ms guest agents, want them queried in parallel", elapsed)
	}
	for i, vm := range vms {
		if got := f.ip(t, 10, vm.id); got != vm.ip {
			t.Errorf("VM %d IP = %q, want %q", i, got, vm.ip)
		}
	}
}

// Guest-agent answers are reused across refreshes: found addresses for
// guestAgentIPTTL, failures for the shorter guestAgentFailureTTL. Stopped
// VMs are never asked.
func TestRangeRefreshCachesGuestAgentAddresses(t *testing.T) {
	f := newRefreshFixture(t, map[string][]fakeVM{"OWNER": {
		{id: 201, name: "OWNER-up", running: true, ip: "10.10.10.1"},
		{id: 202, name: "OWNER-no-agent", running: true},
		{id: 203, name: "OWNER-off", running: false, ip: "10.10.10.3"},
	}})
	agentCalls := func() [3]int {
		return [3]int{f.proxmox.count(agentPath(201)), f.proxmox.count(agentPath(202)), f.proxmox.count(agentPath(203))}
	}
	check := func(step string, want [3]int) {
		t.Helper()
		f.refresh(t, nil, 10)
		if got := agentCalls(); got != want {
			t.Fatalf("%s: guest-agent calls (up, no agent, off) = %v, want %v", step, got, want)
		}
		if got := f.ip(t, 10, 201); got != "10.10.10.1" {
			t.Fatalf("%s: IP = %q, want the agent's address", step, got)
		}
		if got := f.ip(t, 10, 202); got != "null" {
			t.Fatalf("%s: IP without agent = %q, want null", step, got)
		}
	}
	check("first refresh", [3]int{1, 1, 0})
	check("next refresh", [3]int{1, 1, 0})
	f.clock = f.clock.Add(guestAgentFailureTTL + time.Second)
	check("after the failure TTL", [3]int{1, 2, 0})
	f.clock = f.clock.Add(guestAgentIPTTL)
	check("after the address TTL", [3]int{2, 3, 0})
}
