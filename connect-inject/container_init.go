package connectinject

import (
	"bytes"
	"path/filepath"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
)

type initContainerCommandData struct {
	PodName       string
	ServiceName   string
	ServicePort   int32
	Upstreams     []initContainerCommandUpstreamData
	HttpTLS       bool
	GrpcTLS       bool
	TLSServerName string
}

type initContainerCommandUpstreamData struct {
	Name      string
	LocalPort int32
}

// containerInit returns the init container spec for registering the Consul
// service, setting up the Envoy bootstrap, etc.
func (h *Handler) containerInit(pod *corev1.Pod) (corev1.Container, error) {
	data := initContainerCommandData{
		PodName:       pod.Name,
		ServiceName:   pod.Annotations[annotationService],
		HttpTLS:       h.ConsulHTTPSSL,
		GrpcTLS:       h.ConsulGRPCSSL,
		TLSServerName: h.ConsulTLSServerName,
	}
	if data.ServiceName == "" {
		// Assertion, since we call defaultAnnotations above and do
		// not mutate pods without a service specified.
		panic("No service found. This should be impossible since we default it.")
	}

	// If a port is specified, then we determine the value of that port
	// and register that port for the host service.
	if raw, ok := pod.Annotations[annotationPort]; ok && raw != "" {
		if port, _ := portValue(pod, raw); port > 0 {
			data.ServicePort = port
		}
	}

	// If upstreams are specified, configure those
	if raw, ok := pod.Annotations[annotationUpstreams]; ok && raw != "" {
		for _, raw := range strings.Split(raw, ",") {
			parts := strings.SplitN(raw, ":", 2)
			port, _ := portValue(pod, strings.TrimSpace(parts[1]))
			if port > 0 {
				data.Upstreams = append(data.Upstreams, initContainerCommandUpstreamData{
					Name:      strings.TrimSpace(parts[0]),
					LocalPort: port,
				})
			}
		}
	}

	// Render the command
	var buf bytes.Buffer
	tpl := template.Must(template.New("root").Parse(strings.TrimSpace(initContainerCommandTpl)))
	if err := tpl.Execute(&buf, &data); err != nil {
		return corev1.Container{}, err
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
		{
			Name: "POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"},
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

	return corev1.Container{
		Name:         "consul-connect-inject-init",
		Image:        h.ImageConsul,
		Env:          env,
		VolumeMounts: volumeMounts,
		Command:      []string{"/bin/sh", "-ec", buf.String()},
	}, nil
}

// initContainerCommandTpl is the template for the command executed by
// the init container.
const initContainerCommandTpl = `
export CONSUL_HTTP_ADDR="{{ if .HttpTLS -}}https://{{ end -}}${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="{{ if .GrpcTLS -}}https://{{ end -}}${HOST_IP}:8502"
{{ if .TLSServerName -}}
export CONSUL_TLS_SERVER_NAME="{{ .TLSServerName }}"
{{ end -}}

# Register the service. The HCL is stored in the volume so that
# the preStop hook can access it to deregister the service.
cat <<EOF >/consul/connect-inject/service.hcl
services {
  id   = "{{ .PodName }}-{{ .ServiceName }}-proxy"
  name = "{{ .ServiceName }}-proxy"
  kind = "connect-proxy"
  address = "${POD_IP}"
  port = 20000

  proxy {
    destination_service_name = "{{ .ServiceName }}"
    destination_service_id = "{{ .ServiceName}}"
    {{ if (gt .ServicePort 0) -}}
    local_service_address = "127.0.0.1"
    local_service_port = {{ .ServicePort }}
    {{ end -}}


    {{ range .Upstreams -}}
    upstreams {
      destination_name = "{{ .Name }}"
      local_bind_port = {{ .LocalPort }}
    }
    {{ end }}
  }

  checks {
    name = "Proxy Public Listener"
    tcp = "${POD_IP}:20000"
    interval = "10s"
    deregister_critical_service_after = "10m"
  }

  checks {
    name = "Destination Alias"
    alias_service = "{{ .ServiceName }}"
  }
}
EOF

/bin/consul services register /consul/connect-inject/service.hcl

# Generate the envoy bootstrap code
/bin/consul connect envoy \
  -proxy-id="{{ .PodName }}-{{ .ServiceName }}-proxy" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml

# Copy the Consul binary
cp /bin/consul /consul/connect-inject/consul
`
