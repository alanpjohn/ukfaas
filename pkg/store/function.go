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
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/openfaas/faas-provider/types"
	mplatform "kraftkit.sh/machine/platform"
	oci "kraftkit.sh/oci"
	"kraftkit.sh/oci/handler"
	"kraftkit.sh/pack"
	"kraftkit.sh/unikraft/target"
)

type FunctionStore struct {
	handle *handler.ContainerdHandler
	client *containerd.Client

	imageStore            sync.Map
	functionMetaDataMapV2 sync.Map
}

func NewFunctionStore(ctx context.Context, containerdAddr string, defaultNamespace string) (*FunctionStore, error) {
	client, err := containerd.New(containerdAddr, containerd.WithDefaultNamespace(defaultNamespace))
	if err != nil {
		return nil, err
	}

	_, handle, err := handler.NewContainerdWithClient(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("handler initialisation failed with: %w", err)
	}

	return &FunctionStore{
		handle:                handle,
		client:                client,
		imageStore:            sync.Map{},
		functionMetaDataMapV2: sync.Map{},
	}, nil
}

func (f *FunctionStore) Close() {
	f.client.Close()
}

func (f *FunctionStore) NamespaceService() store.Store {
	return f.client.NamespaceService()
}

func (f *FunctionStore) FunctionExists(serviceName string) bool {
	_, exists := f.functionMetaDataMapV2.Load(serviceName)

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

	var (
		ocipack    pack.Package
		descriptor v1.Descriptor
	)

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
		if fmt.Sprintf("%s:%s", unikernelName, unikernelVersion) == ref.Name() {
			ocipack, err = oci.NewPackageFromOCIManifestSpec(
				ctx,
				f.handle,
				ref.Name(),
				manifest,
			)
			descriptor = manifest.Config
			if err == nil {
				break
			} else {
				log.Printf("[FunctionStore.Addfunction] - ERROR : %v\n", err)
			}
		}
	}

	if ocipack == nil {
		return FunctionMetaData{}, fmt.Errorf("no manifests found")
	}

	digest := descriptor.Digest.String()[4:]

	var storageDir string
	if val, exists := f.imageStore.Load(digest); exists {
		ocipack = val.(pack.Package)
		log.Printf("[FunctionStore.Addfunction] - Image already pulled: %s\n", ocipack.Name())
		storageDir = filepath.Join(pkg.OCIDirectory, digest)
	} else {
		storageDir = filepath.Join(pkg.OCIDirectory, digest)
		if err := os.MkdirAll(storageDir, 0o755); err != nil {
			return FunctionMetaData{}, err
		}

		defer func() {
			if err != nil {
				os.RemoveAll(storageDir)
			}
		}()

		platform, _, err := mplatform.Detect(ctx)
		if err != nil {
			return FunctionMetaData{}, err
		}

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

	}

	_, ok := ocipack.(target.Target)
	if !ok {
		log.Printf("[FunctionStore.Addfunction] - package does not convert to target: %s\n", ocipack.Name())
		return FunctionMetaData{}, fmt.Errorf("package does not convert to target")
	}

	log.Printf("[FunctionStore.Addfunction] - Adding to FunctionStore: %s", req.Service)
	functionMetadata := FunctionMetaData{
		FunctionDeployment: req,
		Package:            ocipack,
		StorageDir:         storageDir,
		CreatedAt:          time.Now(),
	}

	f.functionMetaDataMapV2.Store(req.Service, functionMetadata)
	f.imageStore.Store(digest, ocipack)

	return functionMetadata, nil
}

func (f *FunctionStore) DeleteFunction(service string) error {
	f.functionMetaDataMapV2.Delete(service)

	return nil
}

func (f *FunctionStore) ListFunctions() ([]types.FunctionStatus, error) {
	functions := []types.FunctionStatus{}
	f.functionMetaDataMapV2.Range(func(key, value any) bool {
		fName, ok := key.(string)
		fMetadata, fOk := value.(FunctionMetaData)
		if !ok || !fOk {
			return false
		}
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
		return true
	})
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
	val, exists := f.functionMetaDataMapV2.Load(service)
	fMetadata, valid := val.(FunctionMetaData)
	if exists && valid {
		return fMetadata, nil
	}

	return FunctionMetaData{}, fmt.Errorf("function %s not found", service)
}

func (f *FunctionStore) UpdateFunction(ctx context.Context, req types.FunctionDeployment) (FunctionMetaData, bool, error) {
	fMetadata, err := f.GetFunction(req.Service)
	if err != nil {
		return FunctionMetaData{}, false, err
	}

	updateImage := false
	if fMetadata.Image != req.Image {
		updateImage = true
	}

	if updateImage {
		f.DeleteFunction(req.Service)
		funcMeta, err := f.AddFunction(ctx, req)
		return funcMeta, updateImage, err
	}

	updatedFunc := FunctionMetaData{
		FunctionDeployment: req,
		Package:            fMetadata.Package,
		StorageDir:         fMetadata.StorageDir,
	}

	f.functionMetaDataMapV2.Store(req.Service, updatedFunc)

	return updatedFunc, false, nil
}
