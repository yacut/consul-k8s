package connectinject

import (
	"bytes"
	"html/template"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

type sidecarContainerCommandData struct {
	HttpTLS          bool
	TLSServerName    string
	PreferWanAddress bool
}

func (h *Handler) containerSidecar(pod *corev1.Pod) (corev1.Container, error) {
	data := sidecarContainerCommandData{
		HttpTLS:          h.ConsulHTTPSSL,
		TLSServerName:    h.ConsulTLSServerName,
		PreferWanAddress: h.PreferWanAddress,
	}

	volumeMounts := []corev1.VolumeMount{
		corev1.VolumeMount{
			Name:      volumeName,
			MountPath: "/consul/connect-inject",
		},
	}

	env := []corev1.EnvVar{
		{
			Name: "HOST_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.hostIP"},
			},
		},
	}

	if parts := strings.SplitN(h.ConsulCACert, ":", 2); len(parts) == 2 {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      volumeNameCA,
			MountPath: filepath.Dir(parts[1]),
		})
		env = append(env, corev1.EnvVar{
			Name:  "CONSUL_CACERT",
			Value: parts[1],
		})
	}

	// Render the command
	var buf bytes.Buffer
	tpl := template.Must(template.New("root").Parse(strings.TrimSpace(sidecarPreStopCommandTpl)))
	if err := tpl.Execute(&buf, &data); err != nil {
		return corev1.Container{}, err
	}

	return corev1.Container{
		Name:         "consul-connect-envoy-sidecar",
		Image:        h.ImageEnvoy,
		Env:          env,
		VolumeMounts: volumeMounts,
		Lifecycle: &corev1.Lifecycle{
			PreStop: &corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{"/bin/sh", "-ec", buf.String()},
				},
			},
		},
		Command: []string{
			"envoy",
			"--config-path", "/consul/connect-inject/envoy-bootstrap.yaml",
		},
	}, nil
}

const sidecarPreStopCommandTpl = `
{{ if not .PreferWanAddress -}}
export CONSUL_HTTP_ADDR="{{ if .HttpTLS -}}https://{{ end -}}${HOST_IP}:8500"
{{ if .TLSServerName -}}
export CONSUL_TLS_SERVER_NAME="{{ .TLSServerName }}"
{{ end -}}
/consul/connect-inject/consul services deregister \
  /consul/connect-inject/service.hcl
{{ end -}}
`
