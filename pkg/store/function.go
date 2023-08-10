package store

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/alanpjohn/uk-faas/pkg"
	"github.com/containerd/containerd"
	store "github.com/containerd/containerd/namespaces"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/openfaas/faas-provider/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	mplatform "kraftkit.sh/machine/platform"
	oci "kraftkit.sh/oci"
	"kraftkit.sh/oci/handler"
	"kraftkit.sh/pack"
	"kraftkit.sh/unikraft/target"
)

type FunctionStore struct {
	lock   sync.RWMutex
	handle *handler.ContainerdHandler
	client *containerd.Client

	functionImageDirMap map[string]string
	functionMetadataMap map[string]FunctionMetaData
}

func NewFunctionStore(ctx context.Context, containerdAddr string, defaultNamespace string) (*FunctionStore, error) {
	// pm, err := oci.NewOCIManager(ctx,
	// 	oci.WithContainerd(ctx, containerdAddr, defaultNamespace),
	// 	oci.WithDefaultRegistries(),
	// 	oci.WithDefaultAuth(),
	// )
	// if err != nil {
	// 	return nil, err
	// }

	client, err := containerd.New(containerdAddr, containerd.WithDefaultNamespace(defaultNamespace))
	if err != nil {
		return nil, err
	}

	_, handle, err := handler.NewContainerdWithClient(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("handler initialisation failed with: %w", err)
	}

	return &FunctionStore{
		handle:              handle,
		client:              client,
		functionImageDirMap: make(map[string]string),
		functionMetadataMap: make(map[string]FunctionMetaData),
	}, nil
}

func (f *FunctionStore) Close() {
	f.client.Close()
}

func (f *FunctionStore) NamespaceService() store.Store {
	return f.client.NamespaceService()
}

func (f *FunctionStore) FunctionExists(serviceName string) bool {
	f.lock.RLock()
	defer f.lock.RUnlock()

	_, exists := f.functionMetadataMap[serviceName]

	return exists
}

func (f *FunctionStore) AddFunction(ctx context.Context, req types.FunctionDeployment) (FunctionMetaData, error) {
	if f.FunctionExists(req.Service) {
		return FunctionMetaData{}, fmt.Errorf("service %s already exists", req.Service)
	}

	log.Printf("[FunctionStore.Addfunction] - Parsing image name: %s\n", req.Image)
	ref, refErr := name.ParseReference(req.Image,
		name.WithDefaultRegistry(oci.DefaultRegistry),
	)
	if refErr != nil {
		return FunctionMetaData{}, fmt.Errorf("name parse failed with: %w", refErr)
	}

	var ocipack pack.Package

	log.Printf("[FunctionStore.Addfunction] - List manifests: %s\n", req.Image)
	manifests, err := f.handle.ListManifests(ctx)
	if err != nil {
		return FunctionMetaData{}, fmt.Errorf("list manifests failed with: %w", err)
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
				f.handle,
				ref.Name(),
				manifest,
			)
			if err == nil {
				break
			}
		}
	}

	if ocipack == nil {
		return FunctionMetaData{}, fmt.Errorf("no manifests found")
	}

	storageDir := filepath.Join(pkg.OCIDirectory, string(uuid.NewUUID()))
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return FunctionMetaData{}, err
	}

	defer func() {
		if err != nil {
			os.RemoveAll(storageDir)
		}
	}()

	platform, _, err := mplatform.Detect(ctx)

	log.Printf("[FunctionStore.Addfunction] - Pulling Image: %s\n", ocipack.Name())
	err = ocipack.Pull(
		ctx,
		pack.WithPullWorkdir(storageDir),
		pack.WithPullPlatform(platform.String()),
	)
	if err != nil {
		return FunctionMetaData{}, err
	}

	log.Printf("[FunctionStore.Addfunction] - Pull Successful: %s\n", ocipack.Name())
	_, ok := ocipack.(target.Target)
	if !ok {
		log.Printf("[FunctionStore.Addfunction] - package does not convert to target: %s\n", ocipack.Name())
		return FunctionMetaData{}, fmt.Errorf("package does not convert to target")
	}

	log.Printf("[FunctionStore.Addfunction] - Waiting for lock")
	f.lock.Lock()
	defer f.lock.Unlock()

	log.Printf("[FunctionStore.Addfunction] - Adding to FunctionStore: %s", req.Service)
	functionMetadata := FunctionMetaData{
		FunctionDeployment: req,
		Package:            ocipack,
		StorageDir:         storageDir,
		CreatedAt:          time.Now(),
	}

	f.functionImageDirMap[req.Service] = storageDir
	f.functionMetadataMap[req.Service] = functionMetadata

	return functionMetadata, nil
}

func (f *FunctionStore) DeleteFunction(service string) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	imageDir, exists := f.functionImageDirMap[service]
	if !exists {
		return fmt.Errorf("function %s not found", service)
	}
	delete(f.functionMetadataMap, service)
	os.RemoveAll(imageDir)

	return nil
}

func (f *FunctionStore) ListFunctions() ([]types.FunctionStatus, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()

	functions := []types.FunctionStatus{}
	for fName, fMetadata := range f.functionMetadataMap {
		functions = append(functions, types.FunctionStatus{
			Name:                   fName,
			Image:                  fMetadata.Image,
			Namespace:              fMetadata.Namespace,
			EnvProcess:             fMetadata.EnvProcess,
			EnvVars:                fMetadata.EnvVars,
			Constraints:            fMetadata.Constraints,
			Secrets:                fMetadata.Secrets,
			Labels:                 fMetadata.Labels,
			Annotations:            fMetadata.Annotations,
			ReadOnlyRootFilesystem: true,
			Limits:                 fMetadata.Limits,
			Requests:               fMetadata.Requests,
		})
	}
	return functions, nil
}

func (f *FunctionStore) GetFunctionStatus(service string) (types.FunctionStatus, error) {

	fMetadata, err := f.GetFunction(service)
	if err != nil {
		return types.FunctionStatus{}, fmt.Errorf("function %s not found", service)
	}

	return types.FunctionStatus{
		Name:                   service,
		Image:                  fMetadata.Image,
		Namespace:              fMetadata.Namespace,
		EnvProcess:             fMetadata.EnvProcess,
		EnvVars:                fMetadata.EnvVars,
		Constraints:            fMetadata.Constraints,
		Secrets:                fMetadata.Secrets,
		Labels:                 fMetadata.Labels,
		Annotations:            fMetadata.Annotations,
		ReadOnlyRootFilesystem: true,
		Limits:                 fMetadata.Limits,
		Requests:               fMetadata.Requests,
		CreatedAt:              fMetadata.CreatedAt,
	}, nil
}

func (f *FunctionStore) GetFunction(service string) (FunctionMetaData, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()

	fMetadata, exists := f.functionMetadataMap[service]
	if exists {
		return fMetadata, nil
	}

	return FunctionMetaData{}, fmt.Errorf("function %s not found", service)
}

func (f *FunctionStore) UpdateFunction(ctx context.Context, req types.FunctionDeployment) (FunctionMetaData, bool, error) {
	f.lock.RLock()
	fMetadata, exists := f.functionMetadataMap[req.Service]
	if !exists {
		return FunctionMetaData{}, false, fmt.Errorf("function %s not found", req.Service)
	}

	updateImage := false
	if fMetadata.Image != req.Image {
		updateImage = true
	}

	f.lock.RUnlock()
	if updateImage {
		f.DeleteFunction(req.Service)
		funcMeta, err := f.AddFunction(ctx, req)
		return funcMeta, updateImage, err
	}

	f.lock.Lock()
	defer f.lock.Unlock()

	updatedFunc := FunctionMetaData{
		FunctionDeployment: req,
		Package:            fMetadata.Package,
		StorageDir:         fMetadata.StorageDir,
	}

	return updatedFunc, false, nil
}
