package store

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	zip "api.zip"
	"github.com/alanpjohn/uk-faas/pkg"
	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	kraftMachine "kraftkit.sh/api/machine/v1alpha1"
	kraftNet "kraftkit.sh/api/network/v1alpha1"
	kraftVol "kraftkit.sh/api/volume/v1alpha1"
	machinename "kraftkit.sh/machine/name"
	bridge "kraftkit.sh/machine/network/bridge"
	mplatform "kraftkit.sh/machine/platform"
	ninefps "kraftkit.sh/machine/volume/9pfs"
	"kraftkit.sh/unikraft/target"
)

type MachineStore struct {
	lock sync.RWMutex

	// Stores machineID mapped to the Function
	// functionMachineMap map[string][]MachineID

	// Stores machineConfiguration by machineID
	machineInstanceMap map[MachineID]*kraftMachine.Machine
	machineNetworkMap  map[MachineID]kraftNet.NetworkInterfaceTemplateSpec
	networkController  networkapi.NetworkController
}

type MachineRequest struct {
	Service      string
	Image        string
	Namespace    string
	Architecture string
	Platform     string
	Kernel       string
	KernelPath   string
	StoragePath  string
	Annotations  *map[string]string
	Labels       *map[string]string
}

func NewMachineStore(caddy networkapi.NetworkController) (*MachineStore, error) {

	return &MachineStore{
		// functionMachineMap: make(map[string][]MachineID),
		networkController:  caddy,
		machineInstanceMap: make(map[MachineID]*zip.Object[kraftMachine.MachineSpec, kraftMachine.MachineStatus]),
		machineNetworkMap:  make(map[MachineID]kraftNet.NetworkInterfaceTemplateSpec),
		// volumeController:   volumeService,
		// networkController:  networkController,
		// machineController:  machineController,
	}, nil
}

func (m *MachineStore) GetMachinesForFunction(service string) ([]kraftMachine.Machine, error) {
	var machines []kraftMachine.Machine
	m.lock.RLock()
	defer m.lock.RUnlock()
	for _, machine := range m.machineInstanceMap {
		if machine.GetObjectMeta().GetLabels()["ukfaas.io/service"] == service {
			machines = append(machines, *machine)
		}
	}
	return machines, fmt.Errorf("function %s not found", service)
}

func (m *MachineStore) StopAllMachines(ctx context.Context, service string) error {
	err := m.ScaleMachinesTo(ctx, service, 0)
	if err != nil {
		return err
	}

	return nil
}

func (m *MachineStore) GetReplicas(service string) uint64 {
	m.lock.RLock()
	defer m.lock.RUnlock()
	count := 0
	for _, machine := range m.machineInstanceMap {
		if machine.GetObjectMeta().GetLabels()["ukfaas.io/service"] == service && machine.Status.State == kraftMachine.MachineStateRunning {
			count += 1
		}
	}
	return uint64(count)
}

func (m *MachineStore) GetAvailableReplicas(service string) uint64 {
	count, _ := m.networkController.AvailableIPs(service)
	return count
}

func (m *MachineStore) ScaleMachinesTo(ctx context.Context, service string, replicas uint64) error {
	var wg sync.WaitGroup
	currReplicas := m.GetReplicas(service)
	if currReplicas < replicas {
		for i := currReplicas; i < replicas; i++ {
			wg.Add(1)
			log.Printf("[MachineStore.ScaleMachinesTo] - Scaling up %s\n", service)
			go func() {
				err := m.CloneMachine(ctx, service)
				if err != nil {
					log.Printf("error: %v", err)
				}
				defer wg.Done()
			}()
		}
	} else if replicas < currReplicas {
		for i := currReplicas; i > replicas; i-- {
			wg.Add(1)
			log.Printf("[MachineStore.ScaleMachinesTo] - Scaling Down %s\n", service)
			go func() {
				err := m.destroyMachine(ctx, service)
				if err != nil {
					log.Printf("error: %v\n", err)
				}
				defer wg.Done()
			}()
		}
	}
	wg.Wait()
	return nil
}

func (m *MachineStore) getFunctionMachine(service string) (*kraftMachine.Machine, error) {
	for _, machine := range m.machineInstanceMap {
		if machine.GetObjectMeta().GetLabels()["ukfaas.io/service"] == service && machine.Status.State == kraftMachine.MachineStateRunning {
			return machine, nil
		}
	}

	return nil, fmt.Errorf("function %s not found", service)
}

func (m *MachineStore) destroyMachine(ctx context.Context, service string) error {
	platform, _, err := mplatform.Detect(ctx)
	if err != nil {
		return err
	}

	machineStrategy, ok := mplatform.Strategies()[platform]
	if !ok {
		return fmt.Errorf("platform %s not supported", platform)
	}
	machineController, err := machineStrategy.NewMachineV1alpha1(ctx)
	if err != nil {
		return err
	}

	m.lock.Lock()
	machine, notFoundErr := m.getFunctionMachine(service)
	if notFoundErr != nil {
		log.Printf("[MachineStore.destroyMachine] - Not found machine for %s\n", service)
		return notFoundErr
	}
	machine.Status.State = kraftMachine.MachineStateUnknown
	mId := MachineID(machine.GetObjectMeta().GetUID())

	log.Printf("[MachineStore.destroyMachine] - Destroying Machine id:%s\n", mId)

	m.machineInstanceMap[mId] = machine
	iface := m.machineNetworkMap[mId]
	m.lock.Unlock()

	// for _, network := range machine.Spec.Networks {
	// 	for _, iface := range network.Interfaces {
	// 		err = m.caddy.DeleteFunctionInstance(service, iface.Spec.IP)
	// 		if err != nil {
	// 			return err
	// 		}
	// 	}
	// }

	err = m.networkController.DeleteServiceIP(service, networkapi.IP(iface.Spec.IP))
	if err != nil {
		return err
	}

	log.Printf("[MachineStore.destroyMachine] - Stopping qemu-system_x86 id:%s\n", mId)
	machine, stopErr := machineController.Stop(ctx, machine)
	if stopErr != nil {
		return stopErr
	}

	log.Printf("[MachineStore.destroyMachine] - Deleting qemu-system_x86 id:%s\n", mId)
	machine, delErr := machineController.Delete(ctx, machine)
	m.lock.Lock()
	defer m.lock.Unlock()

	link, err := netlink.LinkByName(iface.Spec.IfName[:15])
	if err != nil {
		log.Printf("ERROR: Could not find %s - %v", iface.Spec.IfName, err)
		// return fmt.Errorf("could not get %s link: %v", iface.Spec.IfName, err)
	}

	if machine == nil {
		// Bring down the bridge link
		if link != nil {
			if err := netlink.LinkSetDown(link); err != nil {
				return fmt.Errorf("could not bring %s link down: %v", iface.Spec.IfName, err)
			}

			// Delete the bridge link.
			if err := netlink.LinkDel(link); err != nil {
				return fmt.Errorf("could not delete %s link: %v", iface.Spec.IfName, err)
			}
		}
		delete(m.machineNetworkMap, mId)
		delete(m.machineInstanceMap, mId)
	} else {
		m.machineInstanceMap[mId] = machine
	}
	if delErr != nil {
		log.Printf("[MachineStore.destroyMachine] - Destroy Failed id:%s\n", mId)
		return delErr
	}

	return nil
}

func (m *MachineStore) CloneMachine(ctx context.Context, service string) error {
	m.lock.RLock()
	machine, notFoundErr := m.getFunctionMachine(service)

	if notFoundErr != nil {
		log.Printf("[MachineStore.CloneMachine] - Not found machine for %s\n", service)
		return notFoundErr
	}
	m.lock.RUnlock()

	volumedir := machine.GetObjectMeta().GetAnnotations()["ukfaas.io/filesystem"]
	re := regexp.MustCompile(`([\w/-]+)/unikraft/fs0`)

	// Find the first match in the input string
	matches := re.FindStringSubmatch(volumedir)
	if len(matches) != 2 {
		return fmt.Errorf("no storage directory found in the input string")
	}
	// log.Printf("Parsed filesystem location %s\n", matches[1])

	log.Printf("[MachineStore.CloneMachine] - Cloning machine for%s\n", service)
	return m.createMachine(ctx, MachineRequest{
		Service:      machine.GetObjectMeta().GetLabels()["ukfaas.io/service"],
		Image:        machine.GetObjectMeta().GetLabels()["ukfaas.io/image"],
		Namespace:    machine.GetObjectMeta().GetLabels()["ukfaas.io/namespace"],
		Architecture: machine.Spec.Architecture,
		Platform:     machine.Spec.Platform,
		Kernel:       machine.Spec.Kernel,
		KernelPath:   machine.Status.KernelPath,
		StoragePath:  matches[1],
		Annotations:  &machine.ObjectMeta.Annotations,
		Labels:       &machine.ObjectMeta.Annotations,
	})

}

func (m *MachineStore) NewMachine(ctx context.Context, function FunctionMetaData) error {

	req := function.FunctionDeployment
	targ, targErr := function.Package.(target.Target)

	if !targErr {
		return fmt.Errorf("package does not convert to target")
	}

	log.Printf("[MachineStore.NewMachine] - Creating machine for%s\n", req.Service)
	return m.createMachine(ctx, MachineRequest{
		Service:      req.Service,
		Image:        req.Image,
		Namespace:    req.Namespace,
		Architecture: targ.Architecture().Name(),
		Platform:     targ.Platform().Name(),
		Kernel:       fmt.Sprintf("%s://%s", targ.Format(), req.Image),
		KernelPath:   targ.Kernel(),
		StoragePath:  function.StorageDir,
		Annotations:  req.Annotations,
		Labels:       req.Labels,
	})
}

func (m *MachineStore) createMachine(ctx context.Context, mreq MachineRequest) error {
	var err error

	machine := &kraftMachine.Machine{
		ObjectMeta: metav1.ObjectMeta{},
		Spec: kraftMachine.MachineSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
			},
			Emulation: false,
		},
	}

	machine.ObjectMeta.UID = uuid.NewUUID()
	machine.Status.StateDir = filepath.Join(pkg.MachineDirectory, string(machine.ObjectMeta.UID))
	if err := os.MkdirAll(machine.Status.StateDir, 0o755); err != nil {
		return err
	}

	defer func() {
		if err != nil {
			os.RemoveAll(machine.Status.StateDir)
		}
	}()
	machine.Spec.Architecture = mreq.Architecture
	machine.Spec.Platform = mreq.Platform
	machine.Spec.Kernel = mreq.Kernel
	machine.Spec.ApplicationArgs = []string{} // parse args from `req`
	machine.Status.KernelPath = mreq.KernelPath

	log.Printf("[MachineStore.createMachie] - Setting up volumes: %s\n", machine.ObjectMeta.UID)
	machine.Spec.Volumes = []kraftVol.Volume{}
	volumePath := filepath.Join(mreq.StoragePath, "unikraft/fs0")

	volumeService, err := ninefps.NewVolumeServiceV1alpha1(ctx)
	if err != nil {
		return fmt.Errorf("volume service failed")
	}
	fs0, err := volumeService.Create(ctx, &kraftVol.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Name: volumePath,
		},
		Spec: kraftVol.VolumeSpec{
			Driver:   "9pfs",
			Source:   volumePath,
			ReadOnly: false,
		},
	})

	machine.Spec.Volumes = append(machine.Spec.Volumes, *fs0)

	m.lock.Lock()
	defer m.lock.Unlock()

	log.Printf("[MachineStore.createMachie] - Setting up network: %s\n", machine.ObjectMeta.UID)
	networkName := "openfaas0"
	networkController, err := bridge.NewNetworkServiceV1alpha1(ctx)
	found, err := networkController.Get(ctx, &kraftNet.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: networkName,
		},
	})
	if err != nil {
		return err
	}

	newIface := kraftNet.NetworkInterfaceTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			UID: machine.GetObjectMeta().GetUID(),
		},
		Spec: kraftNet.NetworkInterfaceSpec{
			IfName: fmt.Sprintf("%s@if%s", networkName, machine.ObjectMeta.UID),
		},
	}

	if found.Spec.Interfaces == nil {
		found.Spec.Interfaces = []kraftNet.NetworkInterfaceTemplateSpec{}
	}
	found.Spec.Interfaces = append(found.Spec.Interfaces, newIface)

	// Update the network with the new interface.
	found, err = networkController.Update(ctx, found)
	if err != nil {
		return err
	}

	// Only use the single new interface.
	for _, iface := range found.Spec.Interfaces {
		if iface.UID == newIface.UID {
			newIface = iface
			break
		}
	}
	// Set the interface on the machine.
	found.Spec.Interfaces = []kraftNet.NetworkInterfaceTemplateSpec{newIface}

	log.Printf("[MachineStore.createMachie] - Set IP %s for %s\n", newIface.Spec.IP, machine.ObjectMeta.UID)
	machine.Spec.Networks = []kraftNet.NetworkSpec{found.Spec}

	machine.ObjectMeta.Name = machinename.NewRandomMachineName(0)
	if mreq.Annotations != nil {
		machine.ObjectMeta.Annotations = *mreq.Annotations
	} else {
		machine.ObjectMeta.Annotations = make(map[string]string)
	}
	if mreq.Labels != nil {
		machine.ObjectMeta.Labels = *mreq.Labels
	} else {
		machine.ObjectMeta.Labels = make(map[string]string)
	}
	machine.ObjectMeta.Labels["ukfaas.io/service"] = mreq.Service
	machine.ObjectMeta.Labels["ukfaas.io/image"] = mreq.Image
	machine.ObjectMeta.Labels["ukfaas.io/namespace"] = mreq.Namespace
	machine.ObjectMeta.Annotations["ukfaas.io/filesystem"] = volumePath

	platform, _, err := mplatform.Detect(ctx)

	machineStrategy, ok := mplatform.Strategies()[platform]
	if !ok {
		return fmt.Errorf("platform %s not supported", platform)
	}
	machineController, err := machineStrategy.NewMachineV1alpha1(ctx)
	if err != nil {
		return err
	}

	log.Printf("[MachineStore.createMachie] - Create qemu-system-x86_64 process for %s\n", machine.ObjectMeta.UID)
	machine, err = machineController.Create(ctx, machine)
	if err != nil {
		return err
	}

	for _, machineNetwork := range machine.Spec.Networks {
		for _, iface := range machineNetwork.Interfaces {
			go func(ip string) {
				url := fmt.Sprintf("%s:%d", ip, pkg.WatchdogPort)
				client := &http.Client{}
				status := 0

				for status != http.StatusOK {
					req, err := http.NewRequest("GET", fmt.Sprintf("http://%s", url), nil)
					if err != nil {
						time.Sleep(2 * time.Second)
						continue
					}

					resp, err := client.Do(req)
					if err != nil {
						time.Sleep(2 * time.Second)
						continue
					}
					status = resp.StatusCode
					resp.Body.Close()
				}

				m.networkController.AddServiceIP(mreq.Service, networkapi.IP(ip))
			}(iface.Spec.IP)
		}
	}

	log.Printf("[MachineStore.createMachie] - Start qemu-system-x86_64 process for %s\n", machine.ObjectMeta.UID)
	machine, err = machineController.Start(ctx, machine)
	if err != nil {
		return err
	}

	mId := MachineID(machine.GetObjectMeta().GetUID())
	log.Printf("[MachineStore.createMachie] - Status of %s is %s\n", mId, machine.Status.State)
	// if machines, exists := m.functionMachineMap[req.Service]; exists {
	// 	m.functionMachineMap[req.Service] = append(machines, mId)
	// } else {
	// 	m.functionMachineMap[req.Service] = []MachineID{mId}
	// }

	m.machineInstanceMap[mId] = machine
	m.machineNetworkMap[mId] = newIface
	return nil
}
