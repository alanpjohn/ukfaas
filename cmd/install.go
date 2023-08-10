package main

import (
	"fmt"
	"io"
	"os"
	"path"

	systemd "github.com/openfaas/faasd/pkg/systemd"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const workingDirectoryPermission = 0644
const secretDirPermission = 0755

const ukfaaswd = "/var/lib/ukfaasd"

const ukfaasProviderWd = "/var/lib/ukfaasd-provider"

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install ukfaasd",
	RunE:  runInstall,
}

func runInstall(_ *cobra.Command, _ []string) error {
	if err := ensureWorkingDir(path.Join(ukfaaswd, "secrets")); err != nil {
		return err
	}

	if err := ensureWorkingDir(ukfaasProviderWd); err != nil {
		return err
	}

	if basicAuthErr := makeBasicAuthFiles(path.Join(ukfaaswd, "secrets")); basicAuthErr != nil {
		return errors.Wrap(basicAuthErr, "cannot create basic-auth-* files")
	}

	if err := cp("docker-compose.yaml", ukfaaswd); err != nil {
		return err
	}

	if err := cp("prometheus.yml", ukfaaswd); err != nil {
		return err
	}

	if err := cp("resolv.conf", ukfaaswd); err != nil {
		return err
	}

	err := binExists("/usr/local/bin/", "ukfaasd")
	if err != nil {
		return err
	}

	err = systemd.InstallUnit("ukfaasd-provider", map[string]string{
		"Cwd":             ukfaasProviderWd,
		"SecretMountPath": path.Join(ukfaaswd, "secrets")})

	if err != nil {
		return err
	}

	err = systemd.InstallUnit("ukfaasd", map[string]string{"Cwd": ukfaaswd})
	if err != nil {
		return err
	}

	err = systemd.DaemonReload()
	if err != nil {
		return err
	}

	err = systemd.Enable("ukfaasd-provider")
	if err != nil {
		return err
	}

	err = systemd.Enable("ukfaasd")
	if err != nil {
		return err
	}

	err = systemd.Start("ukfaasd-provider")
	if err != nil {
		return err
	}

	err = systemd.Start("ukfaasd")
	if err != nil {
		return err
	}

	fmt.Println(`Check status with:
  sudo journalctl -u ukfaasd --lines 100 -f

Login with:
  sudo -E cat /var/lib/ukfaasd/secrets/basic-auth-password | faas-cli login -s`)

	return nil
}

func binExists(folder, name string) error {
	findPath := path.Join(folder, name)
	if _, err := os.Stat(findPath); err != nil {
		return fmt.Errorf("unable to stat %s, install this binary before continuing", findPath)
	}
	return nil
}
func ensureSecretsDir(folder string) error {
	if _, err := os.Stat(folder); err != nil {
		err = os.MkdirAll(folder, secretDirPermission)
		if err != nil {
			return err
		}
	}

	return nil
}
func ensureWorkingDir(folder string) error {
	if _, err := os.Stat(folder); err != nil {
		err = os.MkdirAll(folder, workingDirectoryPermission)
		if err != nil {
			return err
		}
	}

	return nil
}

func cp(source, destFolder string) error {
	file, err := os.Open(source)
	if err != nil {
		return err

	}
	defer file.Close()

	out, err := os.Create(path.Join(destFolder, source))
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, file)

	return err
}
