package networking

import (
	"fmt"
	"log"
	"os/exec"

	"github.com/coreos/go-iptables/iptables"
)

// Run creates and configures the container network namespace and bridge.
func Run() (err error) {
	networkNamespaceName := "netns0"
	vethName := "veth0"
	cethName := "ceth0"
	containerIP := "172.18.0.10/16"
	networkBridgeIP := "172.18.0.1"
	bridgeInterfaceName := "br0"

	cmdCreateNetns := exec.Command("ip", "netns", "add", networkNamespaceName)
	if output, err := cmdCreateNetns.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create network namespace: %w\nOutput: %s", err, string(output))
	}
	defer func() {
		if err != nil {
			deleteNetworkNamespace(networkNamespaceName)
		}
	}()

	cmdAddVeth := exec.Command("ip", "link", "add", vethName, "type", "veth", "peer", "name", cethName)
	if output, err := cmdAddVeth.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create veth pair: %w\nOutput: %s", err, string(output))
	}

	cmdVethUp := exec.Command("ip", "link", "set", vethName, "up")
	if output, err := cmdVethUp.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to bring veth0 up: %w\nOutput: %s", err, string(output))
	}

	cmdMoveCeth := exec.Command("ip", "link", "set", cethName, "netns", networkNamespaceName)
	if output, err := cmdMoveCeth.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to move ceth0 to namespace: %w\nOutput: %s", err, string(output))
	}

	// add nat rules in ip table to route traffic to the outside network
	ipt, err := iptables.New()
	if err != nil {
		log.Fatal(err)
	}

	// AppendUnique only adds the rule if it doesn't already exist
	err = ipt.AppendUnique("nat", "POSTROUTING",
		"-s", "172.18.0.0/16", "!", "-o", "br0", "-j", "MASQUERADE")
	if err != nil {
		log.Fatal(err)
	}

	script := `
	ip link set lo up
	ip link set ` + cethName + ` up
	ip addr add ` + containerIP + ` dev ` + cethName + ` 
	ip route add default via ` + networkBridgeIP + `
`
	cmdConfigureNetns := exec.Command(
		"nsenter",
		"--net=/run/netns/"+networkNamespaceName,
		"bash",
		"-c",
		script,
	)
	if output, err := cmdConfigureNetns.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to configure network namespace: %w\nOutput: %s", err, string(output))
	}

	cmdCreateBridge := exec.Command("ip", "link", "add", bridgeInterfaceName, "type", "bridge")
	if output, err := cmdCreateBridge.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create bridge: %w\nOutput: %s", err, string(output))
	}
	defer func() {
		if err != nil {
			deleteNetworkBridge(bridgeInterfaceName)
		}
	}()

	cmdBridgeUp := exec.Command("ip", "link", "set", bridgeInterfaceName, "up")
	if output, err := cmdBridgeUp.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to bring bridge up: %w\nOutput: %s", err, string(output))
	}

	cmdBridgeAddr := exec.Command("ip", "addr", "add", networkBridgeIP+"/16", "dev", bridgeInterfaceName)
	if output, err := cmdBridgeAddr.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to assign IP to bridge: %w\nOutput: %s", err, string(output))
	}

	cmdAttachVeth := exec.Command("ip", "link", "set", vethName, "master", bridgeInterfaceName)
	if output, err := cmdAttachVeth.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to connect veth0 to bridge: %w\nOutput: %s", err, string(output))
	}

	fmt.Println("Network setup complete")
	return nil
}

func deleteNetworkNamespace(networkNamespace string) {
	cmd := exec.Command("ip", "netns", "delete", networkNamespace)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("Failed to delete network namespace: %v\nOutput: %s", err, output)
	}
}

func deleteNetworkBridge(networkBridge string) {
	cmd := exec.Command("ip", "link", "delete", networkBridge)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("Failed to delete network bridge: %v\nOutput: %s", err, output)
	}
}
