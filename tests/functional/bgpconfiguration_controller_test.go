/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package functional_test

import (
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"k8s.io/apimachinery/pkg/types"

	frrk8sv1 "github.com/metallb/frr-k8s/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("BGPConfiguration controller", func() {
	var bgpcfgName types.NamespacedName
	var meallbFRRCfgName types.NamespacedName
	frrCfgNamespace := "metallb-system"

	When("a default BGPConfiguration gets created", func() {
		BeforeEach(func() {
			bgpcfg := CreateBGPConfiguration(namespace, GetBGPConfigurationSpec(""))
			bgpcfgName.Name = bgpcfg.GetName()
			bgpcfgName.Namespace = bgpcfg.GetNamespace()
			DeferCleanup(th.DeleteInstance, bgpcfg)
		})

		It("should have created a BGPConfiguration with default FRRConfigurationNamespace", func() {
			Eventually(func(g Gomega) {
				bgpcfg := GetBGPConfiguration(bgpcfgName)
				g.Expect(bgpcfg).To(Not(BeNil()))
				g.Expect(bgpcfg.Spec.FRRConfigurationNamespace).To(Equal(frrCfgNamespace))
			}, timeout, interval).Should(Succeed())
		})
	})

	When("A pod with NAD gets created but node FRR reference configuration missing", func() {
		var podFrrName types.NamespacedName
		var podName types.NamespacedName
		var metallbNS *corev1.Namespace

		BeforeEach(func() {
			metallbNS = th.CreateNamespace(frrCfgNamespace + "-" + namespace)

			// create a nad config with gateway
			nad := th.CreateNAD(types.NamespacedName{Namespace: namespace, Name: "internalapi"}, GetNADSpec())

			bgpcfg := CreateBGPConfiguration(namespace, GetBGPConfigurationSpec(metallbNS.Name))
			bgpcfgName.Name = bgpcfg.GetName()
			bgpcfgName.Namespace = bgpcfg.GetNamespace()

			podName = types.NamespacedName{Namespace: namespace, Name: uuid.New().String()}
			// create pod with NAD annotation
			th.CreatePod(podName, GetPodAnnotation(namespace), GetPodSpec("worker-0"))
			th.SimulatePodPhaseRunning(podName)

			podFrrName.Name = podName.Namespace + "-" + podName.Name
			podFrrName.Namespace = frrCfgNamespace

			DeferCleanup(th.DeleteInstance, bgpcfg)
			DeferCleanup(th.DeleteInstance, nad)
		})

		It("should NOT have created a FRRConfiguration for the pod", func() {
			pod := th.GetPod(podName)
			Expect(pod).To(Not(BeNil()))

			frr := &frrk8sv1.FRRConfiguration{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, podFrrName, frr)).Should(Not(Succeed()))
			}, timeout, interval).Should(Succeed())
		})
	})

	When("a pod gets created", func() {
		var podFrrName types.NamespacedName
		var podName types.NamespacedName
		var metallbNS *corev1.Namespace

		BeforeEach(func() {
			metallbNS = th.CreateNamespace(frrCfgNamespace + "-" + namespace)
			// create a FRR configuration for a node
			meallbFRRCfgName = types.NamespacedName{Namespace: metallbNS.Name, Name: "worker-0"}
			meallbFRRCfg := CreateFRRConfiguration(meallbFRRCfgName, GetMetalLBFRRConfigurationSpec("worker-0"))
			Expect(meallbFRRCfg).To(Not(BeNil()))

			// TODO test without GW?
			// create a nad config with gateway
			nad := th.CreateNAD(types.NamespacedName{Namespace: namespace, Name: "internalapi"}, GetNADSpec())

			bgpcfg := CreateBGPConfiguration(namespace, GetBGPConfigurationSpec(metallbNS.Name))
			bgpcfgName.Name = bgpcfg.GetName()
			bgpcfgName.Namespace = bgpcfg.GetNamespace()

			podName = types.NamespacedName{Namespace: namespace, Name: uuid.New().String()}
			// create pod without NAD annotation
			th.CreatePod(podName, map[string]string{}, GetPodSpec("worker-0"))
			th.SimulatePodPhaseRunning(podName)

			podFrrName.Name = podName.Namespace + "-" + podName.Name
			podFrrName.Namespace = metallbNS.Name

			DeferCleanup(th.DeleteInstance, bgpcfg)
			DeferCleanup(th.DeleteInstance, nad)
			DeferCleanup(th.DeleteInstance, meallbFRRCfg)
		})

		It("should NOT have created a FRRConfiguration for the pod", func() {
			pod := th.GetPod(podName)
			Expect(pod).To(Not(BeNil()))

			frr := &frrk8sv1.FRRConfiguration{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, podFrrName, frr)).Should(Not(Succeed()))
			}, timeout, interval).Should(Succeed())
		})

		When("NAD annotation gets added to the pod", func() {
			BeforeEach(func() {
				pod := th.GetPod(podName)
				Expect(pod).To(Not(BeNil()))

				pod.Annotations = GetPodAnnotation(namespace)
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Update(ctx, pod)).Should(Succeed())
				}, timeout, interval).Should(Succeed())
			})

			It("should have created a FRRConfiguration for the pod", func() {
				pod := th.GetPod(podName)
				Expect(pod).To(Not(BeNil()))

				podFrrName := podName.Namespace + "-" + podName.Name
				Eventually(func(g Gomega) {
					frr := GetFRRConfiguration(types.NamespacedName{Namespace: metallbNS.Name, Name: podFrrName})
					g.Expect(frr).To(Not(BeNil()))
					g.Expect(frr.Spec.BGP.Routers[0].Prefixes[0]).To(Equal("172.17.0.40/32"))
				}, timeout, interval).Should(Succeed())

			})
		})

		When("another pod with NAD annotation gets created", func() {
			var podName types.NamespacedName

			BeforeEach(func() {
				podName = types.NamespacedName{Namespace: namespace, Name: uuid.New().String()}
				// create pod with NAD annotation
				th.CreatePod(podName, GetPodAnnotation(namespace), GetPodSpec("worker-0"))
				th.SimulatePodPhaseRunning(podName)
			})

			It("should have created a FRRConfiguration for the pod2", func() {
				pod := th.GetPod(podName)
				Expect(pod).To(Not(BeNil()))

				podFrrName := podName.Namespace + "-" + podName.Name
				Eventually(func(g Gomega) {
					frr := GetFRRConfiguration(types.NamespacedName{Namespace: metallbNS.Name, Name: podFrrName})
					g.Expect(frr).To(Not(BeNil()))
					g.Expect(frr.Spec.BGP.Routers[0].Prefixes[0]).To(Equal("172.17.0.40/32"))
				}, timeout, interval).Should(Succeed())

			})
		})
	})

	When("a pod with NAD gets deleted", func() {
		var podFrrName types.NamespacedName
		var podName types.NamespacedName

		BeforeEach(func() {
			metallbNS := th.CreateNamespace(frrCfgNamespace + "-" + namespace)
			// create a FRR configuration for a node
			meallbFRRCfgName = types.NamespacedName{Namespace: metallbNS.Name, Name: "worker-0"}
			meallbFRRCfg := CreateFRRConfiguration(meallbFRRCfgName, GetMetalLBFRRConfigurationSpec("worker-0"))
			Expect(meallbFRRCfg).To(Not(BeNil()))

			// create a nad config with gateway
			nad := th.CreateNAD(types.NamespacedName{Namespace: namespace, Name: "internalapi"}, GetNADSpec())

			bgpcfg := CreateBGPConfiguration(namespace, GetBGPConfigurationSpec(metallbNS.Name))
			bgpcfgName.Name = bgpcfg.GetName()
			bgpcfgName.Namespace = bgpcfg.GetNamespace()

			podName = types.NamespacedName{Namespace: namespace, Name: uuid.New().String()}
			// create pod with NAD annotation
			th.CreatePod(podName, GetPodAnnotation(namespace), GetPodSpec("worker-0"))
			th.SimulatePodPhaseRunning(podName)

			podFrrName.Name = podName.Namespace + "-" + podName.Name
			podFrrName.Namespace = metallbNS.Name

			DeferCleanup(th.DeleteInstance, bgpcfg)
			DeferCleanup(th.DeleteInstance, nad)
			DeferCleanup(th.DeleteInstance, meallbFRRCfg)
		})

		It("should delete the FRRConfiguration for the pod", func() {
			// delete the pod
			pod := th.GetPod(podName)
			Expect(pod).To(Not(BeNil()))
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Delete(ctx, pod)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// validate that the frr cfg is gone
			frr := &frrk8sv1.FRRConfiguration{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, podFrrName, frr)).Should(Not(Succeed()))
			}, timeout, interval).Should(Succeed())
		})
	})
})