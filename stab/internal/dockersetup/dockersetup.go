// Package dockersetup carries the Docker (and NVIDIA GPU) install instructions
// baked into the binary, so a machine without Docker gets exact setup commands
// instead of a terse "install Docker first". Detection picks NVIDIA vs regular
// based on nvidia-smi presence.
package dockersetup

const regularSetup = `Docker is required but not installed on this machine.

Install Docker Engine (Ubuntu 22.04 / 24.04 / 26.04):

  # prerequisites
  sudo apt-get update
  sudo apt-get install -y ca-certificates curl

  # Docker's official GPG key + repository
  sudo install -m 0755 -d /etc/apt/keyrings
  sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  sudo chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

  # Docker Engine + Compose v2 + Buildx
  sudo apt-get update
  sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

  # add your user to the docker group
  sudo usermod -aG docker "$USER"

Then log out and back in (or run: newgrp docker), and re-run this command.`

const nvidiaSetup = `Docker is required but not installed. An NVIDIA GPU was detected (nvidia-smi),
so install Docker with GPU support:

  # prerequisites + Docker Engine (see steps above, then continue below)
  sudo apt-get update && sudo apt-get install -y ca-certificates curl
  sudo install -m 0755 -d /etc/apt/keyrings
  sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  sudo chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
  sudo apt-get update && sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

  # NVIDIA Container Toolkit
  curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
  curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list > /dev/null
  sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit

  # register the nvidia runtime + restart Docker
  sudo nvidia-ctk runtime configure --runtime=docker
  sudo systemctl restart docker

  # add your user to the docker group
  sudo usermod -aG docker "$USER"

Then log out and back in (or run: newgrp docker), and verify GPU access:
  docker run --rm --gpus all nvidia/cuda:12.8.1-base-ubuntu24.04 nvidia-smi`

// InstallInstructions returns the Docker setup commands for this machine:
// the NVIDIA variant when an NVIDIA GPU is present, the regular one otherwise.
func InstallInstructions(nvidiaGPU bool) string {
	if nvidiaGPU {
		return nvidiaSetup
	}
	return regularSetup
}
