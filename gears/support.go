package gears

import (
	"bufio"
	"fmt"
	d "github.com/fsouza/go-dockerclient"
	"github.com/smarterclayton/geard/config"
	"github.com/smarterclayton/geard/docker"
	"github.com/smarterclayton/geard/selinux"
	"github.com/smarterclayton/geard/utils"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

func InitPreStart(dockerSocket string, gearId Identifier, imageName string) error {
	var err error
	var imgInfo *d.Image

	if _, err = user.Lookup(gearId.LoginFor()); err != nil {
		if _, ok := err.(user.UnknownUserError); !ok {
			return err
		}
		if err = createUser(gearId); err != nil {
			return err
		}
	}

	if imgInfo, err = docker.GetImage(dockerSocket, imageName); err != nil {
		return err
	}

	path := path.Join(gearId.HomePath(), "gear-init.sh")
	u, _ := user.Lookup(gearId.LoginFor())
	file, err := utils.OpenFileExclusive(path, 0700)
	if err != nil {
		fmt.Errorf("gear init pre-start: Unable to open script file: ", err)
		return err
	}
	defer file.Close()

	volumes := make([]string, 0, 10)
	for volPath, _ := range imgInfo.Config.Volumes {
		volumes = append(volumes, volPath)
	}

	gearUser := imgInfo.Config.User
	if gearUser == "" {
		gearUser = "gear"
	}

	if erre := GearInitTemplate.Execute(file, GearInitScript{
		imgInfo.Config.User == "",
		gearUser,
		u.Uid,
		u.Gid,
		strings.Join(imgInfo.Config.Cmd, " "),
		len(volumes) > 0,
		strings.Join(volumes, " "),
	}); erre != nil {
		log.Printf("gear init pre-start: Unable to output template: %+v", erre)
		return erre
	}
	if err := file.Close(); err != nil {
		return err
	}

	return nil
}

func createUser(gearId Identifier) error {
	cmd := exec.Command("/usr/sbin/useradd", gearId.LoginFor(), "-m", "-d", gearId.HomePath(), "-c", "Gear user")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Println(out)
		return err
	}
	selinux.RestoreCon(gearId.HomePath(), true)
	return nil
}

func InitPostStart(dockerSocket string, gearId Identifier) error {
	var u *user.User
	var container *d.Container
	var err error

	if u, err = user.Lookup(gearId.LoginFor()); err != nil {
		return err
	}

	if _, container, err = docker.GetContainer(dockerSocket, gearId.LoginFor(), true); err != nil {
		return err
	}

	if err = generateAuthorizedKeys(gearId, u, container, true); err != nil {
		return err
	}

	pid, err := docker.ChildProcessForContainer(container)
	if err != nil {
		return err
	}

	if err = enableRoutingInNamespace(pid); err != nil {
		return err
	}

	if file, err := os.Open(gearId.NetworkLinksPathFor()); err == nil {
		defer file.Close()
		log.Printf("PID %d", pid)
		if err := updateNamespaceNetworkLinks(pid, "127.0.0.2", file); err != nil {
			return err
		}
	}

	return nil
}

func GenerateAuthorizedKeys(dockerSocket string, u *user.User) error {
	var id Identifier
	var container *d.Container
	var err error

	if u.Name != "Gear user" {
		return nil
	}
	if id, err = NewIdentifierFromUser(u); err != nil {
		return err
	}
	if _, container, err = docker.GetContainer(dockerSocket, id.LoginFor(), false); err != nil {
		return err
	}
	if err = generateAuthorizedKeys(id, u, container, false); err != nil {
		return err
	}
	return nil
}

func generateAuthorizedKeys(id Identifier, u *user.User, container *d.Container, forceCreate bool) error {
	var err error
	var sshKeys []string
	var destFile *os.File
	var srcFile *os.File

	var authorizedKeysPortSpec string
	if ipAddr, ports, err := getContainerPorts(container); err != nil {
		return err
	} else {
		for _, p := range ports {
			authorizedKeysPortSpec += fmt.Sprintf("permitopen=\"%v:%v\",", ipAddr, p)
		}
	}

	sshKeys, err = filepath.Glob(path.Join(id.SshAccessBasePath(), "*"))
	os.MkdirAll(id.HomePath(), 0700)
	os.Mkdir(path.Join(id.HomePath(), ".ssh"), 0700)
	authKeysPath := path.Join(id.HomePath(), ".ssh", "authorized_keys")
	if _, err = os.Stat(authKeysPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		if forceCreate {
			os.Remove(authKeysPath)
		} else {
			return nil
		}
	}

	if destFile, err = os.Create(authKeysPath); err != nil {
		return err
	}
	defer destFile.Close()
	w := bufio.NewWriter(destFile)

	for _, keyFile := range sshKeys {
		s, _ := os.Stat(keyFile)
		if s.IsDir() {
			continue
		}

		srcFile, err = os.Open(keyFile)
		defer srcFile.Close()
		w.WriteString(fmt.Sprintf("command=\"%v/bin/switchns\",%vno-agent-forwarding,no-X11-forwarding ", config.GearBasePath(), authorizedKeysPortSpec))
		io.Copy(w, srcFile)
		w.WriteString("\n")
	}
	w.Flush()

	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	for _, path := range []string{
		id.HomePath(),
		filepath.Join(id.HomePath(), ".ssh"),
		filepath.Join(id.HomePath(), ".ssh", "authorized_keys"),
	} {
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
	}

	if err := selinux.RestoreCon(id.BaseHomePath(), true); err != nil {
		return err
	}
	return nil
}

func getContainerPorts(container *d.Container) (string, []string, error) {
	ipAddr := container.NetworkSettings.IPAddress
	ports := []string{}
	for pstr, _ := range container.NetworkSettings.Ports {
		ports = append(ports, strings.Split(string(pstr), "/")[0])
	}

	return ipAddr, ports, nil
}

func getHostIPFromNamespace(name string) (*net.IPAddr, error) {
	// Resolve the containers local IP
	cmd := exec.Command("ip", "netns", "exec", name, "hostname", "-I")
	cmd.Stderr = os.Stderr
	source, erro := cmd.Output()
	if erro != nil {
		log.Printf("network_links: Could not read IP for container: %v", erro)
		return nil, erro
	}
	sourceAddr, errr := net.ResolveIPAddr("ip", strings.TrimSpace(string(source)))
	if errr != nil {
		log.Printf("network_links: Host source IP %s does not resolve %v", sourceAddr, errr)
		return nil, errr
	}
	return sourceAddr, nil
}

func updateNamespaceNetworkLinks(pid int, localAddr string, ports io.Reader) error {
	name := "netlink-" + strconv.Itoa(pid)
	nsPath := fmt.Sprintf("/proc/%d/ns/net", pid)
	path := fmt.Sprintf("/var/run/netns/%s", name)
	if err := os.Symlink(nsPath, path); err != nil && !os.IsExist(err) {
		return err
	}
	defer func() {
		os.Remove(path)
	}()

	sourceAddr, errs := getHostIPFromNamespace(name)
	if errs != nil {
		return errs
	}

	// Restore a set of rules to the table
	cmd := exec.Command("ip", "netns", "exec", name, "iptables-restore")
	stdin, errp := cmd.StdinPipe()
	if errp != nil {
		log.Printf("network_links: Could not open pipe to iptables-restore: %v", errp)
		return errp
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		log.Printf("network_links: Could not start iptables-restore: %v", errp)
		return err
	}

	for {
		link := NetworkLink{}
		if _, err := fmt.Fscanf(ports, "%v\t%v\t%s\n", &link.FromPort, &link.ToPort, &link.ToHost); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("network_links: Could not read from network links file: %v", err)
			continue
		}
		if err := link.Check(); err != nil {
			log.Printf("network_links: Link in file is not valid: %v", err)
			continue
		}
		if link.Complete() {
			destAddr, err := net.ResolveIPAddr("ip", link.ToHost)
			if err != nil {
				log.Printf("network_links: Link destination host does not resolve %v", err)
				continue
			}

			data := OutboundNetworkIptables{sourceAddr.String(), localAddr, link.FromPort, destAddr.IP.String(), link.ToPort}
			if err := OutboundNetworkIptablesTemplate.Execute(stdin, &data); err != nil {
				log.Printf("network_links: Unable to write network link rules: %v", err)
				return err
			}
		}
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		log.Printf("network_links: iptables-restore did not successfully complete: %v", err)
		return err
	}
	return nil
}

func enableRoutingInNamespace(pid int) error {
	out, err := exec.Command("nsenter", "-n", "-t", strconv.Itoa(pid), "--", "/sbin/sysctl", "-w", "net.ipv4.conf.all.route_localnet=1").Output()
	if err != nil {
		log.Printf("routing: Unable to enable localnet routing: %v", err)
		log.Printf("output: %v", out)
		return err
	}

	out, err = exec.Command("nsenter", "-n", "-t", strconv.Itoa(pid), "--", "/sbin/sysctl", "-w", "net.ipv4.ip_forward=1").Output()
	if err != nil {
		log.Printf("routing: Unable to enable ip forwarding: %v", err)
		log.Printf("output: %v", out)
		return err
	}

	return nil
}
