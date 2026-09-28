package filesystem

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Run creates and starts a container with the requested resource limits.
func Run() (err error) {
	fmt.Print("Entre the container name : ")
	reader := bufio.NewReader(os.Stdin)
	containerName, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("failed to read container name: %v", err)
	}
	containerName = strings.TrimSpace(containerName)

	fmt.Print("Entre the CPU percentage : ")
	cpuInput, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("failed to read CPU percentage: %v", err)
	}
	cpuInput = strings.TrimSpace(cpuInput)
	cpuPercent, err := strconv.ParseFloat(cpuInput, 64)
	if err != nil {
		log.Fatalf("invalid CPU percentage: %v", err)
	}

	fmt.Print("Entre the RAM (MB) : ")
	ramInput, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("failed to read RAM: %v", err)
	}
	ramInput = strings.TrimSpace(ramInput)
	ramMB, err := strconv.Atoi(ramInput)
	if err != nil {
		log.Fatalf("invalid RAM value: %v", err)
	}

	rootfs := `/opt/` + containerName + `/rootfs`

	if err := os.MkdirAll(rootfs, 0o755); err != nil {
		log.Fatalf("failed to create rootfs directory: %v", err)
	}

	defer exitContainer(containerName)

	if err := exportRootfs(rootfs); err != nil {
		return fmt.Errorf("failed to export rootfs: %w", err)
	}

	if err := createCGroupAndConfigureIt(containerName, cpuPercent, ramMB); err != nil {
		return fmt.Errorf("failed to create and configure cgroup: %w", err)
	}

	cgDir, _ := os.Open("/sys/fs/cgroup/" + containerName)
	defer cgDir.Close()

	script := buildContainerScript(rootfs, containerName)

	cmd := exec.Command("nsenter", "--net=/var/run/netns/netns0",
		"unshare", "--mount", "--cgroup", "--pid", "--uts", "--fork",
		"bash", "-c", script)

	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(cgDir.Fd())}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run unshare: %w", err)
	}

	return nil
}

func createCGroupAndConfigureIt(containerName string, cpuPercent float64, ramMB int) error {
	dir := "/sys/fs/cgroup/" + containerName
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create cgroup directory: %w", err)
	}

	if err := os.WriteFile(dir+"/memory.max", []byte(strconv.Itoa(ramMB)+"M"), 0o644); err != nil {
		return fmt.Errorf("failed to set memory limit: %w", err)
	}

	if err := os.WriteFile(dir+"/memory.swap.max", []byte("0"), 0o644); err != nil {
		return fmt.Errorf("failed to disable swap memory: %w", err)
	}

	if err := os.WriteFile(dir+"/cpu.max", []byte(strconv.Itoa(int(cpuPercent*1000))+" 100000"), 0o644); err != nil {
		return fmt.Errorf("failed to limite the cpu: %w", err)
	}

	if err := os.WriteFile(dir+"/pids.max", []byte("100"), 0o644); err != nil {
		return fmt.Errorf("failed to disable limite max process: %w", err)
	}

	return nil
}

func moveTheProcessToCGroup(containerName string) error {
	procs := "/sys/fs/cgroup/" + containerName + "/cgroup.procs"
	pid := strconv.Itoa(os.Getpid())

	if err := os.WriteFile(procs, []byte(pid), 0o644); err != nil {
		return fmt.Errorf("failed to add process to cgroup: %w", err)
	}
	return nil
}

func exitContainer(containerName string) {
	if err := os.Remove("/sys/fs/cgroup/" + containerName); err != nil && !os.IsNotExist(err) {
		fmt.Println("cgroup cleanup error:", err)
	}

	dir := filepath.Join("/opt", containerName)
	if err := os.RemoveAll(dir); err != nil {
		fmt.Println("Error while trying to clean up container:", err)
		return
	}

	fmt.Println("Directory and its contents deleted")
}

func exportRootfs(rootfs string) error {
	craneCmd := exec.Command("crane", "export", "alpine:3")
	tarCmd := exec.Command("tar", "-xvC", rootfs)

	pipe, err := craneCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	tarCmd.Stdin = pipe
	tarCmd.Stdout = os.Stdout
	tarCmd.Stderr = os.Stderr
	craneCmd.Stderr = os.Stderr

	if err := tarCmd.Start(); err != nil {
		return fmt.Errorf("failed to start tar: %w", err)
	}
	if err := craneCmd.Run(); err != nil {
		return fmt.Errorf("failed to run crane export: %w", err)
	}
	return tarCmd.Wait()
}

func buildContainerScript(rootfs, containerName string) string {
	return fmt.Sprintf(`
			ROOTFS="%s"
			CONTAINER_NAME="%s"
			mount --rbind "$ROOTFS" "$ROOTFS"

			# ============ Create /dev
			mkdir -p "$ROOTFS/dev"
			mount -t tmpfs -o nosuid,strictatime,mode=0755,size=65536k tmpfs "$ROOTFS/dev"

			# Basic device nodes
			mknod -m 666 "$ROOTFS/dev/null" c 1 3
			chown root:root "$ROOTFS/dev/null"
			mknod -m 666 "$ROOTFS/dev/zero" c 1 5
			chown root:root "$ROOTFS/dev/zero"

			# Shared memory
			mkdir -p "$ROOTFS/dev/shm"
			mount -t tmpfs -o nosuid,nodev,noexec,mode=1777,size=67108864 tmpfs "$ROOTFS/dev/shm"

			# Pseudo terminals
			mkdir -p "$ROOTFS/dev/pts"
			mount -t devpts -o newinstance,ptmxmode=0666,mode=0620 devpts "$ROOTFS/dev/pts"

			# POSIX message queues
			mkdir -p "$ROOTFS/dev/mqueue"
			mount -t mqueue -o nosuid,nodev,noexec mqueue "$ROOTFS/dev/mqueue"

			# Symbolic links
			ln -sf /proc/self/fd   "$ROOTFS/dev/fd"
			ln -sf /proc/self/fd/0 "$ROOTFS/dev/stdin"
			ln -sf /proc/self/fd/1 "$ROOTFS/dev/stdout"
			ln -sf /proc/self/fd/2 "$ROOTFS/dev/stderr"
			ln -sf /proc/kcore     "$ROOTFS/dev/core"
			# ============== finish dev

			# create proc
			mkdir -p "$ROOTFS/proc"
			mount -t proc proc "$ROOTFS/proc"
			# =================== finish proc

			# start the sys directory
			mkdir -p "$ROOTFS/sys"
			mount -t sysfs -o ro,nosuid,nodev,noexec sysfs "$ROOTFS/sys"
			mkdir -p "$ROOTFS/sys/fs/cgroup"
			mount -t cgroup2 -o ro,nosuid,nodev,noexec cgroup2 "$ROOTFS/sys/fs/cgroup"
			# ================ finish the sys directory

			# ================ create the /etc/ files
			touch /opt/$CONTAINER_NAME/{hosts,hostname,resolv.conf}

			echo "127.0.1.1 $CONTAINER_NAME" > /opt/$CONTAINER_NAME/hosts
			echo "$CONTAINER_NAME" > /opt/$CONTAINER_NAME/hostname
			echo "nameserver 8.8.8.8
			nameserver 1.1.1.1
			options edns0 trust-ad" > /opt/$CONTAINER_NAME/resolv.conf

			sudo mount --bind /opt/$CONTAINER_NAME/hosts /opt/$CONTAINER_NAME/rootfs/etc/hosts
			sudo mount --bind /opt/$CONTAINER_NAME/hostname /opt/$CONTAINER_NAME/rootfs/etc/hostname
			sudo mount --bind /opt/$CONTAINER_NAME/resolv.conf /opt/$CONTAINER_NAME/rootfs/etc/resolv.conf

			cd "$ROOTFS"
			mkdir .oldroot
			pivot_root . .oldroot
			umount -l /.oldroot
			rm -rf /.oldroot

			# configure the correct hostname in the container
			hostname "$(cat /etc/hostname)"
			exec /bin/sh
`, rootfs, containerName)
}
