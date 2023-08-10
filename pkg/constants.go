package pkg

const (
	// DefaultFunctionNamespace is the default containerd namespace functions are created
	DefaultFunctionNamespace = "openfaas-fn"

	// NamespaceLabel indicates that a namespace is managed by ukfaasd
	NamespaceLabel = "openfaas"

	// ukfaasNamespace is the containerd namespace services are created
	ukfaasNamespace = "openfaas"

	faasServicesPullAlways = false

	defaultSnapshotter = "overlayfs"

	OCIDirectory     = "/tmp/kraftkit/oci"
	MachineDirectory = "/tmp/kraftkit/machines"

	WatchdogPort = 8123
	GatewayPort  = 80
)
