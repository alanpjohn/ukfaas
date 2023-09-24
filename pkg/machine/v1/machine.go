package v1

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/alanpjohn/uk-faas/pkg"
	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
	machineapi "github.com/alanpjohn/uk-faas/pkg/api/machine"
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

const MachineServiceV1Type machineapi.MachineServiceType = "v1"

type MachineServiceV1 struct {
	lock               sync.RWMutex
	healthChecklock    sync.Mutex
	networkServiceType networkapi.NetworkServiceType

	machineInstanceMapv2 sync.Map
	machineNetworkMapv2  sync.Map
	networkService       networkapi.NetworkService
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

func NewMachineServiceV1(_ context.Context, opts ...any) (machineapi.MachineService, error) {
	m := &MachineServiceV1{
		networkServiceType:   "",
		machineInstanceMapv2: sync.Map{},
		machineNetworkMapv2:  sync.Map{},
	}

	for _, opt := range opts {
		opt, valid := opt.(MachineServiceV1Option)
		if !valid {
			return nil, fmt.Errorf("invalid MachineServiceV1 option provided")
		}
		err := opt(m)
		if err != nil {
			return nil, err
		}
	}

	if m.networkServiceType == "" {
		return nil, fmt.Errorf("no NetworkService found initialised for MachineServiceV1")
	}

	return m, nil
}

func (m *MachineServiceV1) NetworkService() networkapi.NetworkService {
	return m.networkService
}

func (m *MachineServiceV1) GetMachines(_ context.Context, service string) ([]machineapi.Machine, error) {
	var (
		machines []machineapi.Machine
		err      error
	)
	m.machineInstanceMapv2.Range(func(key, value any) bool {
		machine, ok := value.(*machineapi.Machine)
		if !ok {
			err = fmt.Errorf("%s is not valid machine", key)
			return false
		}
		if machine.GetObjectMeta().GetLabels()["ukfaas.io/service"] == service {
			machines = append(machines, *machine)
		}
		return true
	})
	return machines, err
}

func (m *MachineServiceV1) StopAllMachines(ctx context.Context, service string) error {
	err := m.ScaleMachinesTo(ctx, service, 0)
	if err != nil {
		return err
	}

	return nil
}

func (m *MachineServiceV1) RunHealthChecks(ctx context.Context) error {
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
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			time.Sleep(15 * time.Second)
			m.machineInstanceMapv2.Range(func(key, value any) bool {
				m.healthChecklock.Lock()
				defer m.healthChecklock.Unlock()
				mId, idOk := key.(string)
				machine, machineOk := value.(*kraftMachine.Machine)
				if !idOk || !machineOk {
					return false
				}
				if machine.Status.State != kraftMachine.MachineStateRunning {
					return true
				}

				newMachine, err := machineController.Get(ctx, machine)

				if err != nil {
					log.Printf("[MachineStore.RunHealthChekcs] - Error : %v\n", err)
					return true
				}
				if newMachine.Status.State != kraftMachine.MachineStateRunning {
					log.Printf("[MachineStore.RunHealthChecks] - Status of %s is %s", mId, newMachine.Status.State)
					m.destroyMachine(ctx, newMachine)
				}
				return true
			})

		}
	}

}

func (m *MachineServiceV1) GetReplicas(ctx context.Context, service string) uint64 {
	count := 0
	m.machineInstanceMapv2.Range(func(key, value any) bool {
		mId, idOk := key.(string)
		machine, machineOk := value.(*kraftMachine.Machine)
		if !idOk || !machineOk {
			log.Printf("[MachineStore.GetReplicas] - Invalid machine entry found %s %v %v", mId, idOk, machineOk)
			return false
		}
		if machine.GetObjectMeta().GetLabels()["ukfaas.io/service"] == service && machine.Status.State == kraftMachine.MachineStateRunning {
			count += 1
		}
		return true
	})
	return uint64(count)
}

func (m *MachineServiceV1) GetAvailableReplicas(ctx context.Context, service string) uint64 {
	count, _ := m.networkService.AvailableIPs(ctx, service)
	return count
}

func (m *MachineServiceV1) ScaleMachinesTo(ctx context.Context, service string, replicas uint64) error {
	m.healthChecklock.Lock()
	defer m.healthChecklock.Unlock()

	var wg sync.WaitGroup
	currReplicas := m.GetReplicas(ctx, service)
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
				err := m.DeleteMachine(ctx, service)
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

func (m *MachineServiceV1) getFunctionMachine(service string) (*kraftMachine.Machine, error) {
	var requestedMachine *kraftMachine.Machine
	m.machineInstanceMapv2.Range(func(key, value any) bool {
		mId, idOk := key.(string)
		machine, machineOk := value.(*kraftMachine.Machine)
		if !idOk || !machineOk {
			log.Printf("[MachineStore.GetReplicas] - Invalid machine entry found %s %v %v", mId, idOk, machineOk)
			return false
		}
		if machine.GetObjectMeta().GetLabels()["ukfaas.io/service"] == service && machine.Status.State == kraftMachine.MachineStateRunning {
			requestedMachine = machine
			return false
		}
		return true
	})
	if requestedMachine != nil {
		return requestedMachine, nil
	}

	return nil, fmt.Errorf("function %s not found", service)
}

func (m *MachineServiceV1) DeleteMachine(ctx context.Context, service string) error {
	machine, notFoundErr := m.getFunctionMachine(service)
	if notFoundErr != nil {
		log.Printf("[MachineStore.destroyMachine] - Not found machine for %s\n", service)
		return notFoundErr
	}
	machine.Status.State = kraftMachine.MachineStateUnknown
	mId := string(machine.GetObjectMeta().GetUID())
	log.Printf("[MachineStore.destroyMachine] - Destroying Machine id:%s\n", mId)
	m.machineInstanceMapv2.Store(mId, machine)
	return m.destroyMachine(ctx, machine)
}

func (m *MachineServiceV1) destroyMachine(ctx context.Context, machine *kraftMachine.Machine) error {
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

	mId := string(machine.GetObjectMeta().GetUID())
	val, exists := m.machineNetworkMapv2.Load(mId)
	if !exists {
		return fmt.Errorf("network interface for %s not found", mId)
	}
	iface, ok := val.(kraftNet.NetworkInterfaceTemplateSpec)
	if !ok {
		return fmt.Errorf("network interface for %s not valid", mId)
	}

	service := machine.GetObjectMeta().GetLabels()["ukfaas.io/service"]
	err = m.networkService.DeleteServiceIP(ctx, service, networkapi.IP(iface.Spec.IP))
	if err != nil {
		return err
	}

	log.Printf("[MachineStore.destroyMachine] - Stopping qemu-system_x86 id:%s\n", mId)
	machine, stopErr := machineController.Stop(ctx, machine)
	if stopErr != nil {
		log.Printf("[MachineStore.destroyMachine] - Error stopping qemu-system_x86 id:%s err:%v\n", mId, stopErr)
	}

	log.Printf("[MachineStore.destroyMachine] - Deleting qemu-system_x86 id:%s\n", mId)
	// machine, delErr := machineController.Delete(ctx, machine)

	link, err := netlink.LinkByName(iface.Spec.IfName[:15])
	if err != nil {
		log.Printf("ERROR: Could not find %s - %v", iface.Spec.IfName, err)
	}

	if machine == nil {
		if link != nil {
			if err := netlink.LinkSetDown(link); err != nil {
				return fmt.Errorf("could not bring %s link down: %v", iface.Spec.IfName, err)
			}

			if err := netlink.LinkDel(link); err != nil {
				return fmt.Errorf("could not delete %s link: %v", iface.Spec.IfName, err)
			}
		}
		m.machineInstanceMapv2.Delete(mId)
		m.machineNetworkMapv2.Delete(mId)
	} else {
		m.machineInstanceMapv2.Store(mId, machine)
	}
	if err != nil {
		log.Printf("[MachineStore.destroyMachine] - Error deleting qemu-system_x86 id:%s err:%v\n", mId, err)
		return err
	}

	return nil
}

func (m *MachineServiceV1) CloneMachine(ctx context.Context, service string) error {
	machine, notFoundErr := m.getFunctionMachine(service)

	if notFoundErr != nil {
		log.Printf("[MachineStore.CloneMachine] - Not found machine for %s\n", service)
		return notFoundErr
	}

	volumedir := machine.GetObjectMeta().GetAnnotations()["ukfaas.io/filesystem"]

	log.Printf("[MachineStore.CloneMachine] - Cloning machine for%s\n", service)
	return m.createMachine(ctx, MachineRequest{
		Service:      machine.GetObjectMeta().GetLabels()["ukfaas.io/service"],
		Image:        machine.GetObjectMeta().GetLabels()["ukfaas.io/image"],
		Namespace:    machine.GetObjectMeta().GetLabels()["ukfaas.io/namespace"],
		Architecture: machine.Spec.Architecture,
		Platform:     machine.Spec.Platform,
		Kernel:       machine.Spec.Kernel,
		KernelPath:   machine.Status.KernelPath,
		StoragePath:  volumedir,
		Annotations:  &machine.ObjectMeta.Annotations,
		Labels:       &machine.ObjectMeta.Annotations,
	})

}

func (m *MachineServiceV1) NewMachine(ctx context.Context, function functionapi.Function) error {

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
		StoragePath:  filepath.Join(function.StorageDir, "unikraft/fs0"),
		Annotations:  req.Annotations,
		Labels:       req.Labels,
	})
}

func (m *MachineServiceV1) createMachine(ctx context.Context, mreq MachineRequest) error {
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

	var volumeLayer string
	if _, err := os.Stat(mreq.StoragePath); err != nil {
		log.Printf("[MachineStore.createMachine] - No volumes found: %s\n", machine.ObjectMeta.UID)
	} else {
		log.Printf("[MachineStore.createMachine] - Setting up volumes: %s\n", machine.ObjectMeta.UID)
		machine.Spec.Volumes = []kraftVol.Volume{}

		volumeLayer = mreq.StoragePath
		if err := os.MkdirAll(filepath.Join(machine.Status.StateDir, "unikraft"), 0o755); err != nil {
			return err
		}
		volumePath := filepath.Join(machine.Status.StateDir, "unikraft/fs0")
		err = copyDirectory(volumeLayer, volumePath)
		if err != nil {
			return fmt.Errorf("failed to copy volume directory : %v", err)
		}

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

		if err != nil {
			return fmt.Errorf("volume creation failed: %v", err)
		}

		log.Printf("MachineStore.createMachine] Attaching %s as volume", fs0.Spec.Source)

		machine.Spec.Volumes = append(machine.Spec.Volumes, *fs0)
	}

	m.lock.Lock()

	log.Printf("[MachineStore.createMachine] - Setting up network: %s\n", machine.ObjectMeta.UID)
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

	log.Printf("[MachineStore.createMachine] - Set IP %s for %s\n", newIface.Spec.IP, machine.ObjectMeta.UID)
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
	machine.ObjectMeta.Annotations["ukfaas.io/filesystem"] = volumeLayer

	platform, _, err := mplatform.Detect(ctx)

	machineStrategy, ok := mplatform.Strategies()[platform]
	if !ok {
		return fmt.Errorf("platform %s not supported", platform)
	}
	machineController, err := machineStrategy.NewMachineV1alpha1(ctx)
	if err != nil {
		return err
	}

	log.Printf("[MachineStore.createMachine] - Create qemu-system-x86_64 process for %s\n", machine.ObjectMeta.UID)
	machine, err = machineController.Create(ctx, machine)
	if err != nil {
		return err
	}
	m.lock.Unlock()

	for _, machineNetwork := range machine.Spec.Networks {
		for _, iface := range machineNetwork.Interfaces {
			go func(ip string) {
				url := fmt.Sprintf("%s:%d", ip, pkg.WatchdogPort)
				client := &http.Client{}

				for {
					req, err := http.NewRequest("GET", fmt.Sprintf("http://%s", url), nil)
					if err != nil {
						time.Sleep(500 * time.Millisecond)
						continue
					}

					resp, err := client.Do(req)
					if err != nil {
						time.Sleep(500 * time.Millisecond)
						continue
					}
					resp.Body.Close()
					break
				}

				m.networkService.AddServiceIP(ctx, mreq.Service, networkapi.IP(ip))
			}(iface.Spec.IP)
		}
	}

	log.Printf("[MachineStore.createMachine] - Start qemu-system-x86_64 process for %s\n", machine.ObjectMeta.UID)
	machine, err = machineController.Start(ctx, machine)
	if err != nil {
		return err
	}

	mId := string(machine.GetObjectMeta().GetUID())
	log.Printf("[MachineStore.createMachine] - Status of %s is %s\n", mId, machine.Status.State)

	m.machineInstanceMapv2.Store(mId, machine)
	m.machineNetworkMapv2.Store(mId, newIface)
	return nil
}

func copyDirectory(src, dest string) error {
	cmd := exec.Command("cp", "-rT", src, dest)
	log.Printf("[CopyDirectory] %s", cmd.String())
	err := cmd.Run()
	return err
}
