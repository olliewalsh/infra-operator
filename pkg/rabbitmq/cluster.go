package rabbitmq

import (
	"fmt"
	"strings"

	networkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	rabbitmqv1 "github.com/openstack-k8s-operators/infra-operator/apis/rabbitmq/v1beta1"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	rabbitmqv2 "github.com/rabbitmq/cluster-operator/v2/api/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func Cluster(
	r *rabbitmqv1.RabbitMq,
	IPv6Enabled bool,
	fipsEnabled bool,
	topology *topologyv1.Topology,
) *rabbitmqv2.RabbitmqCluster {

	rabbitmq := &rabbitmqv2.RabbitmqCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Name,
			Namespace: r.Namespace,
		},
	}

	envVars := []corev1.EnvVar{
		{
			// The upstream rabbitmq image has /var/log/rabbitmq mode 777, so when
			// openshift runs the rabbitmq container as a random uid it can still write
			// the logs there.  The OSP image however has the directory more constrained,
			// so the random uid cannot write the logs there.  Force it into /var/lib
			// where it can create the file without crashing.
			Name:  "RABBITMQ_UPGRADE_LOG",
			Value: "/var/lib/rabbitmq/rabbitmq_upgrade.log",
		},
		{
			// For some reason HOME needs to be explicitly set here even though the entry
			// for the random user in /etc/passwd has the correct homedir set.
			Name:  "HOME",
			Value: "/var/lib/rabbitmq",
		},
		{
			// The various /usr/sbin/rabbitmq* scripts are really all the same
			// wrapper shell-script that performs some "sanity checks" and then
			// invokes the corresponding "real" program in
			// /usr/lib/rabbitmq/bin.  The main "sanity check" is to ensure that
			// the user running the command is either root or rabbitmq.  Inside
			// of an openshift pod, however, the user is neither of these, so
			// the wrapper script will always fail.

			// By putting the real programs ahead of the wrapper in PATH we can
			// avoid the unnecessary check and just run things directly as
			// whatever user the pod has graciously generated for us.
			Name:  "PATH",
			Value: "/usr/lib/rabbitmq/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		},
	}

	inetFamily := "inet"
	inetProtocol := "tcp"
	tlsArgs := ""
	fipsArgs := ""
	if IPv6Enabled {
		inetFamily = "inet6"
	}
	erlangInetConfig := fmt.Sprintf("{%s,true}.\n", inetFamily)

	if r.Spec.TLS.SecretName != "" {
		inetProtocol = "tls"
		tlsArgs = "-ssl_dist_optfile /etc/rabbitmq/inter-node-tls.config"
		if fipsEnabled {
			fipsArgs = "-crypto fips_mode true"
		}
	}

	envVars = append(envVars, corev1.EnvVar{
		Name: "RABBITMQ_SERVER_ADDITIONAL_ERL_ARGS",
		Value: fmt.Sprintf(
			"-kernel inetrc '/etc/rabbitmq/erl_inetrc' -proto_dist %s_%s %s %s",
			inetFamily,
			inetProtocol,
			tlsArgs,
			fipsArgs,
		),
	}, corev1.EnvVar{
		Name:  "RABBITMQ_CTL_ERL_ARGS",
		Value: fmt.Sprintf("-proto_dist %s_%s %s", inetFamily, inetProtocol, tlsArgs),
	})

	defaultStatefulSet := rabbitmqv2.StatefulSet{
		Spec: &rabbitmqv2.StatefulSetSpec{
			Template: &rabbitmqv2.PodTemplateSpec{
				EmbeddedObjectMeta: &rabbitmqv2.EmbeddedObjectMeta{},
				Spec: &corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{},
					Containers: []corev1.Container{
						{
							// NOTE(gibi): if this is set according to the
							// RabbitMQCluster name the the Pod will crash
							Name:  "rabbitmq",
							Image: r.Spec.ContainerImage,
							Env:   envVars,
							Args: []string{
								// OSP17 runs kolla_start here, instead just run rabbitmq-server directly
								"/usr/lib/rabbitmq/bin/rabbitmq-server",
							},
						},
					},
					InitContainers: []corev1.Container{
						{Name: "setup-container", SecurityContext: &corev1.SecurityContext{}}},
				},
			},
		},
	}

	rabbitmq.Spec.Image = r.Spec.ContainerImage
	rabbitmq.Spec.Override.StatefulSet = r.Spec.Override.StatefulSet
	if rabbitmq.Spec.Override.StatefulSet == nil {
		rabbitmq.Spec.Override.StatefulSet = &defaultStatefulSet
	}

	if rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.NodeSelector == nil {
		if r.Spec.NodeSelector != nil {
			rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.NodeSelector = *r.Spec.NodeSelector
		}
	}

	if r.Spec.Resources != nil {
		rabbitmq.Spec.Resources = r.Spec.Resources
	}

	if topology != nil {
		// Get the Topology .Spec
		ts := topology.Spec
		// Process TopologySpreadConstraints if defined in the referenced Topology
		if ts.TopologySpreadConstraints != nil {
			rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.TopologySpreadConstraints = *topology.Spec.TopologySpreadConstraints
		}
		// Process Affinity if defined in the referenced Topology
		if ts.Affinity != nil {
			rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.Affinity = ts.Affinity
		}
	}
	if r.Spec.Affinity != nil {
		rabbitmq.Spec.Affinity = r.Spec.Affinity
	} else {
		rabbitmq.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
					{
						Weight: 100,
						PodAffinityTerm: corev1.PodAffinityTerm{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "app.kubernetes.io/name",
										Operator: metav1.LabelSelectorOpIn,
										Values: []string{
											rabbitmq.Name,
										},
									},
								},
							},
							TopologyKey: "kubernetes.io/hostname",
						},
					},
				},
			},
		}
	}
	rabbitmq.Spec.Rabbitmq.ErlangInetConfig = erlangInetConfig
	rabbitmq.Spec.Rabbitmq.AdvancedConfig = ""
	rabbitmq.Spec.TLS.CaSecretName = ""
	rabbitmq.Spec.TLS.SecretName = ""
	rabbitmq.Spec.TLS.DisableNonTLSListeners = false
	rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.Volumes = []corev1.Volume{}
	rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{}

	if r.Spec.TLS.SecretName != "" {
		rabbitmq.Spec.TLS.CaSecretName = r.Spec.TLS.SecretName
		rabbitmq.Spec.TLS.SecretName = r.Spec.TLS.CaSecretName
		// disable non tls listeners
		rabbitmq.Spec.TLS.DisableNonTLSListeners = true
		// NOTE(dciabrin) RabbitMQ/Erlang needs a specific TLS configuration ordering
		// in ssl_options.versions for TLS to work with FIPS. We cannot enforce the right
		// ordering with AdditionalConfig, we have to pass a specific Erlang value via
		// the AdvancedConfig field. We also add configuration flags which were known to
		// work with FIPS in previous version of Openstack.
		rabbitmq.Spec.Rabbitmq.AdvancedConfig = `[
{rabbit, [
{ssl_options, [
  {cacertfile,"/etc/rabbitmq-tls/ca.crt"},
  {certfile,"/etc/rabbitmq-tls/tls.crt"},
  {keyfile,"/etc/rabbitmq-tls/tls.key"},
  {depth,1},
  {secure_renegotiate,true},
  {reuse_sessions,true},
  {honor_cipher_order,false},
  {honor_ecc_order,false},
  {verify,verify_none},
  {fail_if_no_peer_cert,false},
  {versions, ['tlsv1.2','tlsv1.3']}
]}
]},
{rabbitmq_management, [
{ssl_config, [
  {ip,"::"},
  {cacertfile,"/etc/rabbitmq-tls/ca.crt"},
  {certfile,"/etc/rabbitmq-tls/tls.crt"},
  {keyfile,"/etc/rabbitmq-tls/tls.key"},
  {depth,1},
  {secure_renegotiate,true},
  {reuse_sessions,true},
  {honor_cipher_order,false},
  {honor_ecc_order,false},
  {verify,verify_none},
  {fail_if_no_peer_cert,false},
  {versions, ['tlsv1.2','tlsv1.3']}
]}
]},
{client, [
{cacertfile, "/etc/rabbitmq-tls/ca.crt"},
{verify,verify_peer},
{secure_renegotiate,true},
{versions, ['tlsv1.2','tlsv1.3']}
]}
].
`

		rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.Volumes = append(
			rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "config-data",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: fmt.Sprintf("%s-config-data", r.Name),
						},
						DefaultMode: ptr.To[int32](0o420),
						Items: []corev1.KeyToPath{
							{
								Key:  "inter_node_tls.config",
								Path: "inter_node_tls.config",
							},
						},
					},
				},
			},
		)
		rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			rabbitmq.Spec.Override.StatefulSet.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				MountPath: "/etc/rabbitmq/inter-node-tls.config",
				ReadOnly:  true,
				Name:      "config-data",
				SubPath:   "inter_node_tls.config",
			},
		)
	}

	if rabbitmq.Spec.Override.Service != nil &&
		rabbitmq.Spec.Override.Service.Spec != nil &&
		rabbitmq.Spec.Override.Service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		if rabbitmq.Spec.Override.Service.EmbeddedLabelsAnnotations == nil {
			rabbitmq.Spec.Override.Service.EmbeddedLabelsAnnotations = &rabbitmqv2.EmbeddedLabelsAnnotations{}
		}

		// add annotation to register service name in dnsmasq
		hostname := fmt.Sprintf("%s.%s.svc", rabbitmq.Name, rabbitmq.Namespace)
		rabbitmq.Spec.Override.Service.Annotations =
			util.MergeStringMaps(rabbitmq.Spec.Override.Service.Annotations,
				map[string]string{networkv1.AnnotationHostnameKey: hostname})
	}

	rabbitmq.Spec.Persistence.StorageClassName = &r.Spec.StorageClass
	if rabbitmq.Spec.Rabbitmq.AdditionalConfig == "" {
		// This is the same situation as RABBITMQ_UPGRADE_LOG above,
		// except for the "main" rabbitmq log we can just force it to use the console.

		// By default the prometheus and management endpoints always bind to ipv4.
		// We need to set the correct address based on the IP version in use.
		var settings []string
		settings = append(settings, "log.console = true")
		settings = append(settings, "prometheus.tcp.ip = ::")
		settings = append(settings, "management.tcp.ip = ::")
		if r.Spec.TLS.SecretName != "" {
			settings = append(settings, "ssl_options.verify = verify_none")
			settings = append(settings, "prometheus.ssl.ip = ::")
			// management ssl ip needs to be set in the AdvancedConfig
		}
		rabbitmq.Spec.Rabbitmq.AdditionalConfig = strings.Join(settings, "\n")
	}

	return rabbitmq
}
