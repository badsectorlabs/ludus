package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/luthermonson/go-proxmox"
	"gopkg.in/yaml.v3"
)

var compiledRangeIDRegex = regexp.MustCompile(`(?i){{\s*range_id\s*}}`)
var lxcIPRegex = regexp.MustCompile(`ip=(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})`)

// ludusEnv is the snapshot of Ludus-related environment + range config used while
// building an inventory. Loaded once per invocation to avoid re-reading the YAML
// file and re-querying env vars per VM.
type ludusEnv struct {
	RangeID         string
	RangeNumber     string
	UserIsAdmin     bool
	ReturnAllRanges bool
	VMs             []LudusVM // resolved from the range config file, may be nil
}

func envTrue(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "true", "1", "yes":
		return true
	}
	return false
}

func loadLudusEnv() ludusEnv {
	e := ludusEnv{
		RangeID:         os.Getenv("LUDUS_RANGE_ID"),
		RangeNumber:     os.Getenv("LUDUS_RANGE_NUMBER"),
		UserIsAdmin:     envTrue("LUDUS_USER_IS_ADMIN"),
		ReturnAllRanges: envTrue("LUDUS_RETURN_ALL_RANGES"),
	}
	if path := os.Getenv("LUDUS_RANGE_CONFIG"); path != "" && e.RangeID != "" && e.RangeNumber != "" {
		if data, err := os.ReadFile(path); err == nil {
			var cfg LudusConfig
			if yaml.Unmarshal(data, &cfg) == nil {
				e.VMs = cfg.Ludus
			}
		}
	}
	return e
}

// findLudusVM returns the matching VM entry (resolved against the active range_id)
// or nil if not configured / not present.
func (e ludusEnv) findLudusVM(vmName string) *LudusVM {
	if e.RangeID == "" {
		return nil
	}
	for i := range e.VMs {
		resolved := compiledRangeIDRegex.ReplaceAllString(e.VMs[i].VMName, e.RangeID)
		if resolved == vmName {
			return &e.VMs[i]
		}
	}
	return nil
}

type Options struct {
	List     bool
	Host     string
	URL      string
	Username string
	Password string
	Token    string
	Secret   string
	Pretty   bool
	Validate bool
}

// enabledOSConfig accepts the boolean and mapping forms used by range-config
// OS keys. A mapping enables the OS because this inventory only needs the key's
// presence; provisioning consumes the mapping's contents elsewhere.
type enabledOSConfig bool

func (o *enabledOSConfig) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var enabled bool
		if err := value.Decode(&enabled); err != nil {
			return err
		}
		*o = enabledOSConfig(enabled)
	case yaml.MappingNode:
		*o = true
	default:
		return fmt.Errorf("expected a boolean or mapping")
	}
	return nil
}

type LudusVM struct {
	VMName      string          `yaml:"vm_name"`
	VLAN        int             `yaml:"vlan"`
	IPLastOctet int             `yaml:"ip_last_octet"`
	ForceIP     bool            `yaml:"force_ip"`
	Windows     enabledOSConfig `yaml:"windows"`
	Linux       enabledOSConfig `yaml:"linux"`
	MacOS       bool            `yaml:"macOS"`
}

type LudusConfig struct {
	Ludus []LudusVM `yaml:"ludus"`
}

func main() {
	opts := Options{}

	flag.BoolVar(&opts.List, "list", false, "List all hosts")
	flag.StringVar(&opts.Host, "host", "", "Get variables for a specific host")
	flag.StringVar(&opts.URL, "url", os.Getenv("PROXMOX_URL"), "Proxmox URL")
	flag.StringVar(&opts.Username, "username", os.Getenv("PROXMOX_USERNAME"), "Proxmox Username")
	flag.StringVar(&opts.Password, "password", os.Getenv("PROXMOX_PASSWORD"), "Proxmox Password")
	flag.StringVar(&opts.Token, "token", os.Getenv("PROXMOX_TOKEN"), "Proxmox API Token ID")
	flag.StringVar(&opts.Secret, "secret", os.Getenv("PROXMOX_SECRET"), "Proxmox API Secret")
	flag.BoolVar(&opts.Pretty, "pretty", false, "Pretty print JSON")
	trustInvalidCerts := flag.Bool("trust-invalid-certs", envTrue("PROXMOX_INVALID_CERT"), "Trust invalid certificates")
	flag.Parse()

	opts.Validate = !*trustInvalidCerts

	if opts.URL == "" || opts.Username == "" || (opts.Password == "" && (opts.Token == "" || opts.Secret == "")) {
		fmt.Fprintln(os.Stderr, "Missing mandatory parameters. Check --url, --username, and auth method (--password OR --token and --secret).")
		os.Exit(1)
	}

	if !strings.HasSuffix(opts.URL, "/") {
		opts.URL += "/"
	}

	// Proxmox Client Setup
	clientOpts := []proxmox.Option{}

	if !opts.Validate {
		insecureClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
		clientOpts = append(clientOpts, proxmox.WithHTTPClient(insecureClient))
	}

	if opts.Token != "" && opts.Secret != "" {
		clientOpts = append(clientOpts, proxmox.WithAPIToken(opts.Token, opts.Secret))
	} else {
		// Native go-proxmox login
		clientOpts = append(clientOpts, proxmox.WithCredentials(&proxmox.Credentials{
			Username: opts.Username,
			Password: opts.Password,
		}))
	}

	client := proxmox.NewClient(opts.URL+"api2/json", clientOpts...)
	ctx := context.Background()

	// Authenticate if using password
	if opts.Token == "" || opts.Secret == "" {
		if err := client.CreateSession(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Auth failed: %v\n", err)
			os.Exit(1)
		}
	}

	var output interface{}
	if opts.List {
		output = mainList(ctx, client)
	} else if opts.Host != "" {
		output = mainHost(ctx, client, opts.Host)
	} else {
		flag.Usage()
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	if opts.Pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to encode output: %v\n", err)
		os.Exit(1)
	}
}

func mainList(ctx context.Context, client *proxmox.Client) map[string]interface{} {
	results := make(map[string]interface{})
	allHosts := []string{}
	hostVars := make(map[string]map[string]interface{})

	env := loadLudusEnv()
	groupMap, poolGroups, validVMIDs := loadPoolState(ctx, client, env)

	// 1 CALL: All cluster resources, filtered server-side to qemu/lxc
	resources, err := fetchVMResources(ctx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get cluster resources: %v\n", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, 20) // Concurrent worker pool

	for _, res := range resources {
		if res.Type != "qemu" && res.Type != "lxc" && res.Type != "openvz" {
			continue
		}

		vmidStr := fmt.Sprintf("%d", res.VMID)

		// Strict range filtering early exit to completely prevent leakage
		if rangeFilterEnabled(env) && !validVMIDs[vmidStr] {
			continue
		}

		wg.Add(1)
		semaphore <- struct{}{}
		go func(r *proxmox.ClusterResource) {
			defer wg.Done()
			defer func() { <-semaphore }()

			vmName, isTemplate, hvars := buildHostVars(ctx, client, env, r)

			mu.Lock()
			hostVars[vmName] = hvars
			if !isTemplate {
				// Templates are omitted from allHosts
				allHosts = append(allHosts, vmName)
			}
			mu.Unlock()
		}(res)
	}

	wg.Wait()

	// Build Dynamic Groups from resolved hostVars
	for hName, hVars := range hostVars {
		// Group from Notes
		if grps, ok := hVars["groups"].([]interface{}); ok {
			for _, g := range grps {
				gStr := fmt.Sprintf("%v", g)
				groupMap[gStr] = append(groupMap[gStr], hName)
			}
		}

		// Group running
		if fmt.Sprintf("%v", hVars["proxmox_status"]) == "running" {
			groupMap["running"] = append(groupMap["running"], hName)
		}

		// Group by OS
		if osID, ok := hVars["proxmox_os_id"]; ok && osID != "" {
			osStr := fmt.Sprintf("%v", osID)
			groupMap[osStr] = append(groupMap[osStr], hName)
		}
	}

	// Final Assemble
	results["all"] = map[string]interface{}{"hosts": allHosts}
	results["_meta"] = map[string]interface{}{"hostvars": hostVars}

	// Clean up groups: ONLY include hosts that made it into hostVars and are NOT templates
	for gName, gHosts := range groupMap {
		var validHosts []string
		seen := make(map[string]bool)

		for _, h := range gHosts {
			if hVars, ok := hostVars[h]; ok && !seen[h] {
				isTmpl := false
				if tmpl, hasTmpl := hVars["proxmox_template"]; hasTmpl && fmt.Sprintf("%v", tmpl) == "1" {
					isTmpl = true
				}

				if !isTmpl {
					validHosts = append(validHosts, h)
					seen[h] = true
				}
			}
		}

		if validHosts == nil {
			validHosts = []string{}
		}

		// Emit groups with visible hosts, plus visible Proxmox pools that should
		// remain addressable even when they are empty.
		if len(validHosts) > 0 || poolGroups[gName] {
			results[gName] = map[string]interface{}{"hosts": validHosts}
		}
	}

	return results
}

func loadPoolState(ctx context.Context, client *proxmox.Client, env ludusEnv) (map[string][]string, map[string]bool, map[string]bool) {
	groupMap := make(map[string][]string)
	poolGroups := make(map[string]bool)
	validVMIDs := make(map[string]bool)

	// Ensure the requested range always exists as a group even if the pool is
	// missing or empty. This keeps playbook limits stable without exposing other
	// users' pool names.
	if env.RangeID != "" {
		groupMap[env.RangeID] = []string{}
		poolGroups[env.RangeID] = true
	}

	if rangeFilterEnabled(env) {
		loadPoolMembers(ctx, client, env.RangeID, env, true, groupMap, poolGroups, validVMIDs)
		if env.UserIsAdmin {
			loadPoolMembers(ctx, client, "ADMIN", env, true, groupMap, poolGroups, validVMIDs)
		}
		return groupMap, poolGroups, validVMIDs
	}

	pools, err := client.Pools(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to fetch pools: %v\n", err)
		return groupMap, poolGroups, validVMIDs
	}

	for _, p := range pools {
		poolID := p.PoolID
		if poolID == "" {
			continue
		}

		groupMap[poolID] = []string{}
		poolGroups[poolID] = true
		loadPoolMembers(ctx, client, poolID, env, true, groupMap, poolGroups, validVMIDs)
	}

	return groupMap, poolGroups, validVMIDs
}

func rangeFilterEnabled(env ludusEnv) bool {
	return env.RangeID != "" && !env.ReturnAllRanges
}

func loadPoolMembers(ctx context.Context, client *proxmox.Client, poolID string, env ludusEnv, emitPoolGroup bool, groupMap map[string][]string, poolGroups map[string]bool, validVMIDs map[string]bool) {
	if poolID == "" {
		return
	}
	pool, err := client.Pool(ctx, poolID)
	if err != nil || pool == nil {
		return
	}
	if emitPoolGroup {
		if _, ok := groupMap[poolID]; !ok {
			groupMap[poolID] = []string{}
		}
		poolGroups[poolID] = true
	}
	processMembers(pool.Members, poolID, env, emitPoolGroup, groupMap, validVMIDs)
}

func processMembers(members []proxmox.ClusterResource, poolID string, env ludusEnv, emitPoolGroup bool, groupMap map[string][]string, validVMIDs map[string]bool) {
	for _, m := range members {
		if m.Type != "qemu" && m.Type != "lxc" {
			continue
		}
		if emitPoolGroup && m.Template != 1 {
			groupMap[poolID] = append(groupMap[poolID], m.Name)
		}
		if rangeFilterEnabled(env) && (poolID == env.RangeID || (env.UserIsAdmin && poolID == "ADMIN")) {
			validVMIDs[fmt.Sprintf("%d", m.VMID)] = true
		}
	}
}

// fetchVMResources returns the cluster resources filtered to VMs (qemu + lxc).
// It uses the typed Cluster.Resources wrapper but constructs the Cluster locally
// to avoid the extra /cluster/status call that client.Cluster(ctx) performs.
func fetchVMResources(ctx context.Context, client *proxmox.Client) (proxmox.ClusterResources, error) {
	cluster := (&proxmox.Cluster{}).New(client)
	return cluster.Resources(ctx, "vm")
}

// buildHostVars renders the Ansible hostvars for a single cluster resource, doing
// the per-VM config fetch and (for running qemu) the guest-agent calls. Shared by
// mainList and mainHost so a single-host lookup costs O(1) calls instead of O(N).
func buildHostVars(ctx context.Context, client *proxmox.Client, env ludusEnv, res *proxmox.ClusterResource) (vmName string, isTemplate bool, hvars map[string]interface{}) {
	vmName = res.Name
	nodeName := res.Node
	vmid := int(res.VMID)
	vmType := res.Type
	vmStatus := res.Status
	isTemplate = res.Template == 1

	hvars = make(map[string]interface{})

	// Copy ClusterResource fields as proxmox_<lowercase-json-key>. Round-tripping
	// through json honors omitempty so absent/zero fields stay absent, matching
	// the previous raw-map behavior.
	if b, err := json.Marshal(res); err == nil {
		var raw map[string]interface{}
		if json.Unmarshal(b, &raw) == nil {
			for k, v := range raw {
				hvars["proxmox_"+strings.ToLower(k)] = v
			}
		}
	}

	// Backwards compatibility mappings for Python script behavior
	if maxcpu, ok := hvars["proxmox_maxcpu"]; ok {
		hvars["proxmox_cpus"] = maxcpu
	}
	if mem, ok := hvars["proxmox_mem"]; ok {
		hvars["proxmox_memhost"] = mem
	}

	// 1 CALL: Fetch Config for Description/Notes
	var config map[string]interface{}
	_ = client.Get(ctx, fmt.Sprintf("/nodes/%s/%s/%d/config", nodeName, vmType, vmid), &config)

	desc := ""
	if d, ok := config["description"]; ok {
		desc = fmt.Sprintf("%v", d)
	}
	metadata := parseMetadata(desc)

	if vmType == "qemu" && !isTemplate {
		pveVM := &proxmox.VirtualMachine{}
		pveVM.New(client, nodeName, vmid)

		if vmStatus == "running" {
			ifaces, err := getAgentIfacesWithRetry(ctx, pveVM)
			if err == nil {
				osInfo, _ := pveVM.AgentOsInfo(ctx)
				if osInfo != nil {
					id := osInfo.ID
					if id == "mswindows" {
						id = "windows"
					}
					hvars["proxmox_os_id"] = id
					hvars["proxmox_os_name"] = osInfo.Name
					hvars["proxmox_os_machine"] = osInfo.Machine
					hvars["proxmox_os_kernel"] = osInfo.KernelRelease
					hvars["proxmox_os_version_id"] = osInfo.VersionID
				}

				allIPs := extractIPs(ifaces)
				resolvedIP := checkIPAddresses(env, vmName, allIPs)
				if resolvedIP != "" {
					hvars["ansible_host"] = resolvedIP
				}

				// macOS bypass: agent reports no usable os-id but the VM name says macOS.
				if _, hasOS := hvars["proxmox_os_id"]; !hasOS && strings.Contains(strings.ToLower(vmName), "macos") {
					hvars["proxmox_os_id"] = "macos"
				}
			} else {
				resolvedIP := checkIPAddresses(env, vmName, []string{})
				if resolvedIP != "" {
					hvars["ansible_host"] = resolvedIP
				}
				if osID := getOSInfoFromConfig(env, vmName); osID != "" {
					hvars["proxmox_os_id"] = osID
				}
			}
		}
	} else if vmType != "qemu" && !isTemplate {
		if net0, ok := config["net0"]; ok {
			matches := lxcIPRegex.FindStringSubmatch(fmt.Sprintf("%v", net0))
			if len(matches) > 1 {
				hvars["ansible_host"] = matches[1]
			}
		}
	}

	for k, v := range metadata {
		hvars[k] = v
	}
	return vmName, isTemplate, hvars
}

func mainHost(ctx context.Context, client *proxmox.Client, targetHost string) map[string]interface{} {
	env := loadLudusEnv()
	validVMIDs := map[string]bool{}
	if rangeFilterEnabled(env) {
		_, _, validVMIDs = loadPoolState(ctx, client, env)
	}

	resources, err := fetchVMResources(ctx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get cluster resources: %v\n", err)
		return map[string]interface{}{}
	}

	var target *proxmox.ClusterResource
	for _, r := range resources {
		if r.Name != targetHost {
			continue
		}
		if r.Type == "qemu" || r.Type == "lxc" || r.Type == "openvz" {
			if rangeFilterEnabled(env) && !validVMIDs[fmt.Sprintf("%d", r.VMID)] {
				return map[string]interface{}{}
			}
			target = r
			break
		}
	}
	if target == nil {
		return map[string]interface{}{}
	}

	_, _, hvars := buildHostVars(ctx, client, env, target)
	return hvars
}

func getAgentIfacesWithRetry(ctx context.Context, vm *proxmox.VirtualMachine) ([]*proxmox.AgentNetworkIface, error) {
	ifaces, err := vm.AgentGetNetworkIFaces(ctx)
	if err != nil {
		time.Sleep(500 * time.Millisecond)
		ifaces, err = vm.AgentGetNetworkIFaces(ctx)
	}
	return ifaces, err
}

func parseMetadata(desc string) map[string]interface{} {
	if desc == "" {
		return map[string]interface{}{}
	}

	var meta map[string]interface{}
	err := json.Unmarshal([]byte(desc), &meta)
	if err != nil {
		// Python fallback handles single quotes
		descFix := strings.ReplaceAll(desc, "'", "\"")
		err = json.Unmarshal([]byte(descFix), &meta)
		if err != nil {
			return map[string]interface{}{"notes": desc}
		}
	}
	return meta
}

func extractIPs(ifaces []*proxmox.AgentNetworkIface) []string {
	ips := []string{}
	for _, iface := range ifaces {
		for _, ip := range iface.IPAddresses {
			parsed := net.ParseIP(ip.IPAddress)
			if parsed != nil && parsed.To4() != nil {
				ips = append(ips, ip.IPAddress)
			}
		}
	}
	return ips
}

var (
	network172 = mustCIDR("172.16.0.0/12")
	network192 = mustCIDR("192.0.2.0/24")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func checkIPAddresses(env ludusEnv, vmName string, ipAddresses []string) string {
	var configIP string
	forceIP := false
	if vm := env.findLudusVM(vmName); vm != nil && env.RangeNumber != "" {
		configIP = fmt.Sprintf("10.%s.%d.%d", env.RangeNumber, vm.VLAN, vm.IPLastOctet)
		forceIP = vm.ForceIP
	}

	var validIPs []string
	for _, ipStr := range ipAddresses {
		ip := net.ParseIP(ipStr)
		if ip == nil || ip.To4() == nil {
			continue
		}
		if ipStr == configIP {
			return ipStr
		}
		if !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !network172.Contains(ip) {
			validIPs = append(validIPs, ipStr)
		}
	}

	for _, ipStr := range validIPs {
		if network192.Contains(net.ParseIP(ipStr)) {
			return ipStr
		}
	}

	if len(validIPs) > 0 {
		return validIPs[0]
	}

	if forceIP && configIP != "" {
		return configIP
	}

	return ""
}

func getOSInfoFromConfig(env ludusEnv, vmName string) string {
	vm := env.findLudusVM(vmName)
	if vm == nil {
		return ""
	}
	switch {
	case bool(vm.Windows):
		return "windows"
	case bool(vm.Linux):
		return "linux"
	case vm.MacOS:
		return "macos"
	}
	return ""
}
