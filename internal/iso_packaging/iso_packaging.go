package iso_packaging

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/project-flotta/osbuild-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	templateCommand string = "isopackage %s -ks %s --upload-target %s"
)

var (
	jobTTLafterFinish int32 = 100   // Job will be deleted after finished with this
	deadlineSeconds   int64 = 36000 // Job will be terminated after a hour.
)

// Builder is a struct that manages a build to package an iso
type Builder struct {
	// id        string
	// sourceIso string
	// kickstart io.Reader
	// output    string
	// namespace string

	client      client.Client
	jobSpec     *batchv1.Job
	build       *v1alpha1.OSBuild
	buildConfig *v1alpha1.OSBuildEnvConfig
}

func NewBuilderJob(client client.Client, build *v1alpha1.OSBuild, buildConfig *v1alpha1.OSBuildEnvConfig) *Builder {
	return &Builder{
		client:      client,
		jobSpec:     nil,
		build:       build,
		buildConfig: buildConfig,
		// sourceIso: sourceIso,
		// kickstart: kickstart,
		// output:    output,
		// client:    client,
		// id:        id,
		// namespace: ns,
	}
}

// @TODO BaseImage cannot get that from envs.
// @TODO kickstart should be added via configfile or should be based on that.
func (b *Builder) Start() error {
	sourceISO := b.build.Status.ComposerIso
	if sourceISO == "" {
		return fmt.Errorf("Cannot parse invalid iso image")
	}
	_, err := url.Parse(sourceISO)
	if err != nil {
		return err
	}

	if b.build.Spec.Kickstart == nil {
		return fmt.Errorf("Kickstart is not defined, skipping")
	}

	// command := fmt.Sprintf(templateCommand, b.sourceIso, b.kickstart, b.output)
	command := `sleep 10s`                   //FIXME to remove
	baseImage := "docker.io/curlimages/curl" // FIXME: source image should be defined.

	jobSpec := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      b.build.Name,
			Namespace: b.build.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: b.build.APIVersion,
					Kind:       b.build.Kind,
					Name:       b.build.Name,
				},
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &jobTTLafterFinish,
			ActiveDeadlineSeconds:   &deadlineSeconds,
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:    "isoCommand",
							Image:   baseImage,
							Command: strings.Split(command, " "),
						},
					},
					RestartPolicy: v1.RestartPolicyNever,
					Volumes: []v1.Volume{
						{
							Name: "config",
							VolumeSource: v1.VolumeSource{
								Projected: &v1.ProjectedVolumeSource{
									Sources: []v1.VolumeProjection{{
										ConfigMap: &v1.ConfigMapProjection{
											LocalObjectReference: v1.LocalObjectReference{
												// Name: b.build.Spec.Kickstart,
												Name: "kickstart",
											},
										},
									}},
								},
							},
						},
					},
				},
			},
		},
	}

	if b.buildConfig.Spec.S3Service.AWS != nil {
		// @TODO check here if the secrets can be from other namespace
		jobSpec.Spec.Template.Spec.Volumes[0].VolumeSource.Projected.Sources[0].Secret = &v1.SecretProjection{
			LocalObjectReference: v1.LocalObjectReference{
				Name: b.buildConfig.Spec.S3Service.AWS.CredsSecretReference.Name,
			},
		}
	}

	err = b.client.Create(context.TODO(), jobSpec)
	if err != nil {
		return fmt.Errorf("Cannot applied job: %v", err)
	}
	b.jobSpec = jobSpec
	return nil
}

func (b *Builder) IsFinished() (bool, error) {
	job := batchv1.Job{}
	err := b.client.Get(context.TODO(), client.ObjectKey{
		Namespace: b.jobSpec.Namespace,
		Name:      b.jobSpec.Name,
	}, &job)

	if err != nil {
		return false, fmt.Errorf("Cannot get job: %s", err)
	}

	b.jobSpec = &job
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == v1.ConditionTrue {
			if c.Type == batchv1.JobFailed {
				return true, errors.New("Cannot repackage the ISO image correctly")
			}
			return true, nil
		}
	}
	return false, nil
}

func (b *Builder) Delete() error {
	err := b.client.Delete(context.TODO(), b.jobSpec)
	if err != nil {
		return fmt.Errorf("Cannot delete job: %v", err)
	}
	return nil
}
