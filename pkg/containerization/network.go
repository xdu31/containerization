package containerization

import (
	"fmt"
	"go.uber.org/zap"
	"net"
	"os"
	"time"

	"code.cloudfoundry.org/guardian/kawasaki/netns"
	"github.com/vishvananda/netlink"
)

type NetworkConfigInput struct {
	Pid              int
	Logger           *zap.Logger
	BridgeName       string
	BridgeAddress    string
	ContainerAddress string
	VethNamePrefix   string
}

type NetworkConfig struct {
	BridgeName     string
	BridgeIP       net.IP
	ContainerIP    net.IP
	Subnet         *net.IPNet
	VethNamePrefix string
}

type Configurer interface {
	Apply(netConfig NetworkConfig, pid int) error
}

type Container struct {
	NetnsExecer *netns.Execer
}

type Host struct {
	BridgeCreator BridgeCreator
	VethCreator   VethCreator
}

type BridgeCreator interface {
	Create(name string, ip net.IP, subnet *net.IPNet) (*net.Interface, error)
	Attach(bridge, hostVeth *net.Interface) error
}

type VethCreator interface {
	Create(vethNamePrefix string) (*net.Interface, *net.Interface, error)
	MoveToNetworkNamespace(containerVeth *net.Interface, pid int) error
}

type Bridge struct{}

func NewBridge() *Bridge {
	return &Bridge{}
}

type Veth struct{}

func NewVeth() *Veth {
	return &Veth{}
}

type Netset struct {
	HostConfigurer      Configurer
	ContainerConfigurer Configurer
}

func New(hostConfigurer, containerConfigurer Configurer) *Netset {
	return &Netset{
		HostConfigurer:      hostConfigurer,
		ContainerConfigurer: containerConfigurer,
	}
}

func (b *Bridge) Create(name string, ip net.IP, subnet *net.IPNet) (*net.Interface, error) {
	if interfaceExists(name) {
		return net.InterfaceByName(name)
	}

	linkAttrs := netlink.LinkAttrs{Name: name}
	link := &netlink.Bridge{
		LinkAttrs: linkAttrs,
	}

	if err := netlink.LinkAdd(link); err != nil {
		Logger.Error("LinkAdd1", zap.Error(err))
		return nil, err
	}

	address := &netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: subnet.Mask}}
	if err := netlink.AddrAdd(link, address); err != nil {
		Logger.Error("LinkAdd2", zap.Error(err))
		return nil, err
	}

	if err := netlink.LinkSetUp(link); err != nil {
		Logger.Error("LinkSetUp", zap.Error(err))
		return nil, err
	}

	return net.InterfaceByName(name)
}

func (b *Bridge) Attach(bridge, hostVeth *net.Interface) error {
	bridgeLink, err := netlink.LinkByName(bridge.Name)
	if err != nil {
		return err
	}

	hostVethLink, err := netlink.LinkByName(hostVeth.Name)
	if err != nil {
		return err
	}

	return netlink.LinkSetMaster(hostVethLink, bridgeLink.(*netlink.Bridge))
}

func interfaceExists(name string) bool {
	_, err := net.InterfaceByName(name)

	return err == nil
}

func (v *Veth) Create(namePrefix string) (*net.Interface, *net.Interface, error) {
	hostVethName := fmt.Sprintf("%s0", namePrefix)
	containerVethName := fmt.Sprintf("%s1", namePrefix)

	if interfaceExists(hostVethName) {
		return vethInterfacesByName(hostVethName, containerVethName)
	}

	vethLinkAttrs := netlink.NewLinkAttrs()
	vethLinkAttrs.Name = hostVethName

	veth := &netlink.Veth{
		LinkAttrs: vethLinkAttrs,
		PeerName:  containerVethName,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return nil, nil, err
	}

	if err := netlink.LinkSetUp(veth); err != nil {
		return nil, nil, err
	}

	return vethInterfacesByName(hostVethName, containerVethName)
}

func (v *Veth) MoveToNetworkNamespace(containerVeth *net.Interface, pid int) error {
	containerVethLink, err := netlink.LinkByName(containerVeth.Name)
	if err != nil {
		return err
	}

	return netlink.LinkSetNsPid(containerVethLink, pid)
}

func vethInterfacesByName(
	hostVethName,
	containerVethName string,
) (*net.Interface, *net.Interface, error) {
	hostVeth, err := net.InterfaceByName(hostVethName)
	if err != nil {
		return nil, nil, err
	}

	containerVeth, err := net.InterfaceByName(containerVethName)
	if err != nil {
		return nil, nil, err
	}

	return hostVeth, containerVeth, nil
}

func NewContainerConfigurer(netnsExecer *netns.Execer) *Container {
	return &Container{
		NetnsExecer: netnsExecer,
	}
}

func (c *Container) Apply(netConfig NetworkConfig, pid int) error {
	netnsFile, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", pid))
	defer netnsFile.Close()
	if err != nil {
		return fmt.Errorf(
			"Unable to find network namespace for process with pid '%d'",
			pid,
		)
	}

	cbFunc := func() error {
		containerVethName := fmt.Sprintf("%s1", netConfig.VethNamePrefix)
		link, err := netlink.LinkByName(containerVethName)
		if err != nil {
			return fmt.Errorf("Container veth '%s' not found", containerVethName)
		}

		addr := &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   netConfig.ContainerIP,
				Mask: netConfig.Subnet.Mask,
			},
		}
		err = netlink.AddrAdd(link, addr)
		if err != nil {
			return fmt.Errorf(
				"Unable to assign IP address '%s' to %s",
				netConfig.ContainerIP,
				containerVethName,
			)
		}

		if err := netlink.LinkSetUp(link); err != nil {
			return err
		}

		route := &netlink.Route{
			Scope:     netlink.SCOPE_UNIVERSE,
			LinkIndex: link.Attrs().Index,
			Gw:        netConfig.BridgeIP,
		}

		return netlink.RouteAdd(route)
	}

	return c.NetnsExecer.Exec(netnsFile, cbFunc)
}

func NewHostConfigurer(bridgeCreator BridgeCreator, vethCreator VethCreator) *Host {
	return &Host{
		BridgeCreator: bridgeCreator,
		VethCreator:   vethCreator,
	}
}

func (h *Host) Apply(netConfig NetworkConfig, pid int) error {
	bridge, err := h.BridgeCreator.Create(
		netConfig.BridgeName,
		netConfig.BridgeIP,
		netConfig.Subnet,
	)
	if err != nil {
		Logger.Error("Create error1", zap.Error(err))
		return err
	}

	hostVeth, containerVeth, err := h.VethCreator.Create(netConfig.VethNamePrefix)
	if err != nil {
		Logger.Error("Create error2", zap.Error(err))
		return err
	}

	err = h.BridgeCreator.Attach(bridge, hostVeth)
	if err != nil {
		Logger.Error("Attach error1", zap.Error(err))
		return err
	}

	err = h.VethCreator.MoveToNetworkNamespace(containerVeth, pid)
	if err != nil {
		Logger.Error("MoveToNetworkNamespace error1", zap.Error(err))
		return err
	}

	return nil
}

var Logger *zap.Logger

func SetNetwork(configInput *NetworkConfigInput) error {
	bridgeCreator := NewBridge()
	vethCreator := NewVeth()
	netnsExecer := &netns.Execer{}

	hostConfigurer := NewHostConfigurer(bridgeCreator, vethCreator)
	containerConfigurer := NewContainerConfigurer(netnsExecer)

	Logger = configInput.Logger

	bridgeIP, bridgeSubnet, err := net.ParseCIDR(configInput.BridgeAddress)
	if err != nil {
		Logger.Error("ParseCIDR error1", zap.Error(err))
		return err
	}

	containerIP, _, err := net.ParseCIDR(configInput.ContainerAddress)
	if err != nil {
		Logger.Error("ParseCIDR error2", zap.Error(err))
		return err
	}

	netConfig := NetworkConfig{
		BridgeName:     configInput.BridgeName,
		BridgeIP:       bridgeIP,
		ContainerIP:    containerIP,
		Subnet:         bridgeSubnet,
		VethNamePrefix: configInput.VethNamePrefix,
	}

	err = hostConfigurer.Apply(netConfig, configInput.Pid)
	if err != nil {
		Logger.Error("Apply 1", zap.Error(err))
		return err
	}

	err = containerConfigurer.Apply(netConfig, configInput.Pid)
	if err != nil {
		Logger.Error("Apply 2", zap.Error(err))
		return err
	}

	return nil
}

func WaitForNetwork() error {
	maxWait := time.Second * 3
	checkInterval := time.Second
	timeStarted := time.Now()

	for {
		interfaces, err := net.Interfaces()
		if err != nil {
			return err
		}

		// pretty basic check ...
		// > 1 as a lo device will already exist
		if len(interfaces) > 1 {
			return nil
		}

		if time.Since(timeStarted) > maxWait {
			return fmt.Errorf("Timeout after %s waiting for network", maxWait)
		}

		time.Sleep(checkInterval)
	}
}
