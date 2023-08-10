package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	ioutil "io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/alanpjohn/uk-faas/pkg/store"
	"github.com/containerd/containerd/namespaces"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/openfaas/faas-provider/types"
	"github.com/rancher/wrangler/pkg/signals"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	machineapi "kraftkit.sh/api/machine/v1alpha1"
	networkapi "kraftkit.sh/api/network/v1alpha1"
	volumeapi "kraftkit.sh/api/volume/v1alpha1"
	"kraftkit.sh/iostreams"
	machinename "kraftkit.sh/machine/name"
	bridge "kraftkit.sh/machine/network/bridge"
	mplatform "kraftkit.sh/machine/platform"
	ninefps "kraftkit.sh/machine/volume/9pfs"
	oci "kraftkit.sh/oci"
	handler "kraftkit.sh/oci/handler"
	pack "kraftkit.sh/pack"
	"kraftkit.sh/unikraft/target"
)

func MakeDeployHandler(fStore *store.FunctionStore, mStore *store.MachineStore, secretMountPath string, alwaysPull bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Deploy] request: %s\n", string(body))

		req := types.FunctionDeployment{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Deploy] - error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		namespace := getRequestNamespace(req.Namespace)

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(fStore.NamespaceService(), namespace)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			log.Printf("[Deploy] - error validating namespace: %s\n", err)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			log.Printf("[Deploy] - error validating namespace: %s\n", err)
			return
		}

		namespaceSecretMountPath := getNamespaceSecretMountPath(secretMountPath, namespace)
		err = validateSecrets(namespaceSecretMountPath, req.Secrets)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			log.Printf("[Deploy] - error validating secrets: %s\n", err)
			return
		}

		name := req.Service
		ctx := namespaces.WithNamespace(context.Background(), namespace)

		log.Printf("[Deploy] request: Creating service - %s\n", name)
		function, err := fStore.AddFunction(ctx, req)
		if err != nil {
			log.Printf("[Deploy] error pulling %s, error: %s\n", name, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("[Deploy] request: Creating machine - %s\n", function.Image)
		err = mStore.NewMachine(ctx, function)
		if err != nil {
			log.Printf("[Deploy] error running machine %s, error: %s\n", name, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("[Deploy] request: Deployed service - %s\n", function.Service)
		w.WriteHeader(http.StatusOK)

	}
}

func Deploy(ctx context.Context, req types.FunctionDeployment, secretMountPath string, alwaysPull bool) error {

	// setup machine object
	machine := &machineapi.Machine{
		ObjectMeta: metav1.ObjectMeta{},
		Spec: machineapi.MachineSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
			},
			Emulation: false,
		},
	}

	// setup package manager
	pm, err := oci.NewOCIManager(ctx,
		oci.WithDetectHandler(),
		oci.WithDefaultRegistries(),
		oci.WithDefaultAuth(),
	)
	if err != nil {
		return fmt.Errorf("pm initialisation failed with: %w", err)
	}

	ref, refErr := name.ParseReference(req.Image,
		name.WithDefaultRegistry(oci.DefaultRegistry),
	)
	if refErr != nil {
		return fmt.Errorf("name parse failed with: %w", refErr)
	}

	ctx, handle, err := handler.NewContainerdHandler(ctx, "/run/containerd/containerd.sock", "default")
	if err != nil {
		return fmt.Errorf("handler initialisation failed with: %w", err)
	}

	var ocipack pack.Package

	manifests, err := handle.ListManifests(ctx)
	if err != nil {
		return fmt.Errorf("list manifests failed with: %w", err)
	}

	for _, manifest := range manifests {
		var (
			unikernelName    string
			unikernelVersion string
			ok               bool
			err              error
		)
		if unikernelName, ok = manifest.Annotations["org.unikraft.image.name"]; !ok {
			continue
		}
		if unikernelVersion, ok = manifest.Annotations["org.unikraft.image.version"]; !ok {
			continue
		}
		// log.Println(foundUnikernelName, ref.Name())
		if fmt.Sprintf("%s:%s", unikernelName, unikernelVersion) == ref.Name() {
			ocipack, err = oci.NewPackageFromOCIManifestSpec(
				ctx,
				handle,
				ref.Name(),
				manifest,
			)
			if err == nil {
				break
			}
		}
	}

	if ocipack == nil {
		return fmt.Errorf("no manifests found")
	}

	machine.ObjectMeta.UID = uuid.NewUUID()
	machine.Status.StateDir = filepath.Join("/tmp/kraftkit", string(machine.ObjectMeta.UID))
	if err := os.MkdirAll(machine.Status.StateDir, 0o755); err != nil {
		return err
	}

	defer func() {
		if err != nil {
			os.RemoveAll(machine.Status.StateDir)
		}
	}()

	platform, _, err := mplatform.Detect(ctx)

	err = ocipack.Pull(
		ctx,
		pack.WithPullWorkdir(machine.Status.StateDir),
		pack.WithPullPlatform(platform.String()),
	)
	if err != nil {
		return err
	}

	targ, ok := ocipack.(target.Target)
	if !ok {
		return fmt.Errorf("package does not convert to target")
	}

	machine.Spec.Architecture = targ.Architecture().Name()
	machine.Spec.Platform = targ.Platform().Name()
	machine.Spec.Kernel = fmt.Sprintf("%s://%s", pm.Format(), req.Image)
	machine.Spec.ApplicationArgs = []string{} // parse args from `req`
	machine.Status.KernelPath = targ.Kernel()

	if req.Limits != nil && req.Limits.Memory != "" {
		quantity, err := resource.ParseQuantity(req.Limits.Memory)
		if err != nil {
			return err
		}

		machine.Spec.Resources.Requests[corev1.ResourceMemory] = quantity
	}

	machine.Spec.Volumes = []volumeapi.Volume{}
	volumePath := filepath.Join(machine.Status.StateDir, "unikraft/fs0")

	volumeService, err := ninefps.NewVolumeServiceV1alpha1(ctx)
	if err != nil {
		return fmt.Errorf("volume service failed")
	}
	fs0, err := volumeService.Create(ctx, &volumeapi.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Name: volumePath,
		},
		Spec: volumeapi.VolumeSpec{
			Driver:   "9pfs",
			Source:   volumePath,
			ReadOnly: false, // TODO(nderjung): Options are not yet supported.
		},
	})

	machine.Spec.Volumes = append(machine.Spec.Volumes, *fs0)

	networkName := "docker0"
	networkController, err := bridge.NewNetworkServiceV1alpha1(ctx)
	found, err := networkController.Get(ctx, &networkapi.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: networkName,
		},
	})
	if err != nil {
		return err
	}

	newIface := networkapi.NetworkInterfaceTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			UID: uuid.NewUUID(),
		},
		Spec: networkapi.NetworkInterfaceSpec{},
	}

	if found.Spec.Interfaces == nil {
		found.Spec.Interfaces = []networkapi.NetworkInterfaceTemplateSpec{}
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
	found.Spec.Interfaces = []networkapi.NetworkInterfaceTemplateSpec{newIface}
	machine.Spec.Networks = []networkapi.NetworkSpec{found.Spec}

	machine.ObjectMeta.Name = machinename.NewRandomMachineName(0)

	machineStrategy, ok := mplatform.Strategies()[platform]
	if !ok {
		return fmt.Errorf("platform %s not supported", platform)
	}
	machineController, err := machineStrategy.NewMachineV1alpha1(ctx)
	if err != nil {
		return err
	}

	machine, err = machineController.Create(ctx, machine)
	if err != nil {
		return err
	}

	go func() {
		events, errs, err := machineController.Watch(ctx, machine)
		if err != nil {
			// log.G(ctx).Errorf("could not listen for machine updates: %v", err)
			signals.RequestShutdown()
			return
		}

		// log.G(ctx).Trace("waiting for machine events")

	loop:
		for {
			// Wait on either channel
			select {
			case update := <-events:
				switch update.Status.State {
				case machineapi.MachineStateExited, machineapi.MachineStateFailed:
					signals.RequestShutdown()
					break loop
				}

			case <-errs:
				// log.G(ctx).Errorf("received event error: %v", err)
				signals.RequestShutdown()
				break loop

			case <-ctx.Done():
				break loop
			}
		}

		// Remove the instance on Ctrl+C if the --rm flag is passed
		if _, err := machineController.Stop(ctx, machine); err != nil {
			// log.G(ctx).Errorf("could not stop: %v", err)
			return
		}
		if _, err := machineController.Delete(ctx, machine); err != nil {
			// log.G(ctx).Errorf("could not remove: %v", err)
			return
		}

	}()

	machine, err = machineController.Start(ctx, machine)
	if err != nil {
		signals.RequestShutdown()
		return err
	}

	fmt.Fprintf(iostreams.G(ctx).Out, "%s\n", machine.Name)

	return nil

}

func validateSecrets(secretMountPath string, secrets []string) error {
	for _, secret := range secrets {
		if _, err := os.Stat(path.Join(secretMountPath, secret)); err != nil {
			return fmt.Errorf("unable to find secret: %s", secret)
		}
	}
	return nil
}
