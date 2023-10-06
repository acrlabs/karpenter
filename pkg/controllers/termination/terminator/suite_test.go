/*
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

package terminator_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	. "knative.dev/pkg/logging/testing"

	"github.com/aws/karpenter-core/pkg/controllers/termination/terminator"

	v1 "k8s.io/api/core/v1"

	"github.com/aws/karpenter-core/pkg/apis"
	"github.com/aws/karpenter-core/pkg/apis/settings"
	"github.com/aws/karpenter-core/pkg/operator/scheme"
	"github.com/aws/karpenter-core/pkg/test"
	. "github.com/aws/karpenter-core/pkg/test/expectations"
)

var ctx context.Context
var env *test.Environment
var recorder *test.EventRecorder
var queue *terminator.Queue
var pdb *policyv1.PodDisruptionBudget
var pod *v1.Pod

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Eviction")
}

var _ = BeforeSuite(func() {
	env = test.NewEnvironment(scheme.Scheme, test.WithCRDs(apis.CRDs...))
	ctx = settings.ToContext(ctx, test.Settings(settings.Settings{DriftEnabled: true}))
	recorder = test.NewEventRecorder()
	queue = terminator.NewQueue(env.KubernetesInterface.CoreV1(), recorder)
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	recorder.Reset() // Reset the events that we captured during the run
	// Shut down the queue and restart it to ensure no races
	queue.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

var testLabels = map[string]string{"test": "label"}

var _ = Describe("Eviction/Queue", func() {
	BeforeEach(func() {
		pdb = test.PodDisruptionBudget(test.PDBOptions{
			Labels:         testLabels,
			MaxUnavailable: &intstr.IntOrString{IntVal: 0},
		})
		pod = test.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Labels: testLabels,
			},
		})
	})

	Context("Eviction API", func() {
		It("should succeed with no event when the pod is not found", func() {
			ExpectApplied(ctx, env.Client, pdb)
			Expect(queue.Evict(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})).To(BeTrue())
			Expect(recorder.Events()).To(HaveLen(0))
		})
		It("should succeed with an evicted event when there are no PDBs", func() {
			ExpectApplied(ctx, env.Client, pod)
			Expect(queue.Evict(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})).To(BeTrue())
			Expect(recorder.Calls("Evicted")).To(Equal(1))
		})
		It("should succeed with no event when there are PDBs that allow an eviction", func() {
			pdb = test.PodDisruptionBudget(test.PDBOptions{
				Labels:         testLabels,
				MaxUnavailable: &intstr.IntOrString{IntVal: 1},
			})
			ExpectApplied(ctx, env.Client, pod)
			Expect(queue.Evict(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})).To(BeTrue())
			Expect(recorder.Calls("Evicted")).To(Equal(1))
		})
		It("should return a NodeDrainError event when a PDB is blocking", func() {
			ExpectApplied(ctx, env.Client, pdb, pod)
			Expect(queue.Evict(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})).To(BeFalse())
			Expect(recorder.Calls("FailedDraining")).To(Equal(1))
		})
		It("should fail when two PDBs refer to the same pod", func() {
			pdb2 := test.PodDisruptionBudget(test.PDBOptions{
				Labels:         testLabels,
				MaxUnavailable: &intstr.IntOrString{IntVal: 0},
			})
			ExpectApplied(ctx, env.Client, pdb, pdb2, pod)
			Expect(queue.Evict(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})).To(BeFalse())
		})
	})
})