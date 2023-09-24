package v1

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/alanpjohn/uk-faas/pkg"
	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
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

const FunctionServiceV1Type functionapi.FunctionServiceType = "v1"

type FunctionStoreV1 struct {
	handle *handler.ContainerdHandler
	client *containerd.Client

	imageStore            sync.Map
	functionMetaDataMapV2 sync.Map
}

func NewFunctionStoreV1(ctx context.Context, opts ...any) (functionapi.FunctionService, error) {
	f := &FunctionStoreV1{
		imageStore:            sync.Map{},
		functionMetaDataMapV2: sync.Map{},
	}

	for _, opt := range opts {
		opt, valid := opt.(FunctionServiceV1Option)
		if !valid {
			return nil, fmt.Errorf("invalid FunctionStoreV1 option provided")
		}
		err := opt(f)
		if err != nil {
			return nil, err
		}
	}

	if f.client != nil {
		_, handle, err := handler.NewContainerdWithClient(ctx, f.client)
		if err != nil {
			return nil, fmt.Errorf("handler initialisation failed with: %w", err)
		}
		f.handle = handle
	} else {
		return nil, fmt.Errorf("containerd client not set")
	}

	return f, nil
}

func (f *FunctionStoreV1) Close() {
	f.client.Close()
}

func (f *FunctionStoreV1) NamespaceService() store.Store {
	return f.client.NamespaceService()
}

func (f *FunctionStoreV1) FunctionExists(serviceName string) bool {
	_, exists := f.functionMetaDataMapV2.Load(serviceName)

	return exists
}

func (f *FunctionStoreV1) AddFunction(ctx context.Context, req types.FunctionDeployment) (functionapi.Function, error) {
	if f.FunctionExists(req.Service) {
		return functionapi.Function{}, fmt.Errorf("service %s already exists", req.Service)
	}

	log.Printf("[FunctionStore.Addfunction] - Parsing image name: %s\n", req.Image)
	ref, refErr := name.ParseReference(req.Image,
		name.WithDefaultRegistry(oci.DefaultRegistry),
	)
	if refErr != nil {
		return functionapi.Function{}, fmt.Errorf("name parse failed with: %w", refErr)
	}

	var (
		ocipack    pack.Package
		descriptor v1.Descriptor
	)

	log.Printf("[FunctionStore.Addfunction] - List manifests: %s\n", req.Image)
	manifests, err := f.handle.ListManifests(ctx)
	if err != nil {
		return functionapi.Function{}, fmt.Errorf("list manifests failed with: %w", err)
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
		return functionapi.Function{}, fmt.Errorf("no manifests found")
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
			return functionapi.Function{}, err
		}

		defer func() {
			if err != nil {
				os.RemoveAll(storageDir)
			}
		}()

		platform, _, err := mplatform.Detect(ctx)
		if err != nil {
			return functionapi.Function{}, err
		}

		log.Printf("[FunctionStore.Addfunction] - Pulling Image: %s\n", ocipack.Name())
		err = ocipack.Pull(
			ctx,
			pack.WithPullWorkdir(storageDir),
			pack.WithPullPlatform(platform.String()),
		)
		if err != nil {
			return functionapi.Function{}, err
		}

		log.Printf("[FunctionStore.Addfunction] - Pull Successful: %s\n", ocipack.Name())

	}

	_, ok := ocipack.(target.Target)
	if !ok {
		log.Printf("[FunctionStore.Addfunction] - package does not convert to target: %s\n", ocipack.Name())
		return functionapi.Function{}, fmt.Errorf("package does not convert to target")
	}

	log.Printf("[FunctionStore.Addfunction] - Adding to FunctionStore: %s", req.Service)
	functionMetadata := functionapi.Function{
		FunctionDeployment: req,
		Package:            ocipack,
		StorageDir:         storageDir,
		CreatedAt:          time.Now(),
	}

	f.functionMetaDataMapV2.Store(req.Service, functionMetadata)
	f.imageStore.Store(digest, ocipack)

	return functionMetadata, nil
}

func (f *FunctionStoreV1) DeleteFunction(_ context.Context, service string) error {
	f.functionMetaDataMapV2.Delete(service)

	return nil
}

func (f *FunctionStoreV1) ListFunctions(_ context.Context) ([]types.FunctionStatus, error) {
	functions := []types.FunctionStatus{}
	f.functionMetaDataMapV2.Range(func(key, value any) bool {
		fName, ok := key.(string)
		fMetadata, fOk := value.(functionapi.Function)
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

func (f *FunctionStoreV1) GetFunctionStatus(ctx context.Context, service string) (types.FunctionStatus, error) {
	fMetadata, err := f.GetFunction(ctx, service)
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

func (f *FunctionStoreV1) GetFunction(_ context.Context, service string) (functionapi.Function, error) {
	val, exists := f.functionMetaDataMapV2.Load(service)
	fMetadata, valid := val.(functionapi.Function)
	if exists && valid {
		return fMetadata, nil
	}

	return functionapi.Function{}, fmt.Errorf("function %s not found", service)
}

func (f *FunctionStoreV1) UpdateFunction(ctx context.Context, req types.FunctionDeployment) (functionapi.Function, bool, error) {
	fMetadata, err := f.GetFunction(ctx, req.Service)
	if err != nil {
		return functionapi.Function{}, false, err
	}

	updateImage := false
	if fMetadata.Image != req.Image {
		updateImage = true
	}

	if updateImage {
		f.DeleteFunction(ctx, req.Service)
		funcMeta, err := f.AddFunction(ctx, req)
		return funcMeta, updateImage, err
	}

	updatedFunc := functionapi.Function{
		FunctionDeployment: req,
		Package:            fMetadata.Package,
		StorageDir:         fMetadata.StorageDir,
	}

	f.functionMetaDataMapV2.Store(req.Service, updatedFunc)

	return updatedFunc, false, nil
}
