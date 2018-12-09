package connectinject

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

type sidecarContainerCommandData struct {
	PreferWanAddress bool
}

func (h *Handler) containerSidecar(pod *corev1.Pod) corev1.Container {
	data := initContainerCommandData{
		PreferWanAddress: h.PreferWanAddress,
	}

	container := corev1.Container{
		Name:  "consul-connect-envoy-sidecar",
		Image: h.ImageEnvoy,
		Env: []corev1.EnvVar{
			{
				Name: "HOST_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.hostIP"},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				Name:      volumeName,
				MountPath: "/consul/connect-inject",
			},
		},
		Command: []string{
			"envoy",
			"--config-path", "/consul/connect-inject/envoy-bootstrap.yaml",
		},
	}

	if !data.PreferWanAddress {
		container.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"/bin/sh",
						"-ec",
						strings.TrimSpace(sidecarPreStopCommand),
					},
				},
			},
		}
	}
	return container
}

const sidecarPreStopCommand = `
{{ if not .PreferWanAddress -}}
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
/consul/connect-inject/consul services deregister \
  /consul/connect-inject/service.hcl
{{ end -}}
`
