// Package function defines an interface for the function service component
// and includes related request types.
package function

import (
	"context"
	"time"

	// "github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/namespaces"
	"github.com/openfaas/faas-provider/types"
	"kraftkit.sh/pack"
)

// Function encapsulates all data related to an OpenFaaS function and its corresponding image.
// This includes information about the function's creation time, the function's deployment request,
// the parsed OCI image represented as a [kraftkit.sh/pack.Package], and the storage directory
// where the OCI image has been pulled and unpacked.
type Function struct {

	// CreatedAt stores the time when the Function object was created.
	CreatedAt time.Time

	// FunctionDeployment contains the OpenFaaS function deployment request
	// used to create this Function.
	types.FunctionDeployment

	// Package represents the [kraftkit.sh/pack.Package] parsed after the image
	// for the function has been pulled and unpacked.
	pack.Package

	// StorageDir is the path where the OCI image has been pulled and unpacked.
	StorageDir string
}

// FunctionServiceType is an identifier for the function service
type FunctionServiceType string

// FunctionService handles all CRUD operations on functions and manages the OCI images for functions.
type FunctionService interface {

	// Close gracefully closes all components.
	Close()

	// Returns a service to handle namespaces for function deployment
	NamespaceService() namespaces.Store

	// FunctionExists checks if a function with the given string exists.
	FunctionExists(string) bool

	// AddFunction parses and validates a Function Deployment Request.
	// It also pulls and unpacks the OCI image mentioned in the request.
	//
	// If validation and image pull are successful, it returns a [function.Function],
	// otherwise, it returns an error.
	AddFunction(context.Context, types.FunctionDeployment) (Function, error)

	// DeleteFunction deletes the function with the given function name.
	//
	// It returns an error if the function could not be found.
	DeleteFunction(context.Context, string) error

	// ListFunctions lists all functions currently deployed and returns their status.
	//
	// It returns an error if it cannot retrieve the status of all functions.
	ListFunctions(context.Context) ([]types.FunctionStatus, error)

	// GetFunctionStatus returns the function status of the function with the given function name.
	//
	// It returns an error if the function could not be found.
	GetFunctionStatus(context.Context, string) (types.FunctionStatus, error)

	// GetFunction returns a [function.Function] with the given function name.
	//
	// It returns an error if the function could not be found.
	GetFunction(context.Context, string) (Function, error)

	// UpdateFunction updates the function with the new function deployment.
	//
	// It returns the updated [function.Function] and a boolean that represents
	// whether the function images and resource allotment have been updated.
	//
	// It returns an error if the update fails.
	UpdateFunction(context.Context, types.FunctionDeployment) (Function, bool, error)
}
