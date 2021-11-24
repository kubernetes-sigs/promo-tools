# CIP Checks Interface
The addition of the checks interface to the Container Image Promoter is meant 
to make it easy to add checks against pull requests affecting the promoter 
manifests. The interface allows engineers to add checks without worrying about 
any pre-existing checks and test their own checks individually, while also 
giving freedom as to what conditionals or tags might be necessary for the 
check to occur. Additionally, using an interface means easy expandability of
check requirements in the future.

## Interface Explanation
The `PreCheck` interface is implemented like so in the 
[types.go](/legacy/dockerregistry/types.go)
file. The `Run` function is the method used in order to actually execute the 
check that implements this interface.

```
type PreCheck interface {
	Run() error   
}
```

### How Checks Are Called
A `RunChecks` method has been implemented which iterates over an input list of 
PreChecks and runs them on an input set of promotion edges. `RunChecks` then 
returns any errors that are returned from each `PreCheck`.

```
func (sc *SyncContext) RunChecks(
	checks []PreCheck,
) error {
	//Iterate over checks and execute each one
}
```

#### Integration With PROW
The Container Image Promoter has several Prow jobs that run whenever a pull
request attempts to modify the promoter manifests. The
[*pull-k8sio-cip*][k8sio-presubmits] and the
[*pull-k8sio-cip-vuln*][k8sio-presubmits] Prow jobs call the `RunChecks`
function and actually run their respective checks. New Prow jobs can be
[added][add-prow-job] to run an individual check in the future if that check
requires a separate job.

### How To Add A Check
In order to add a check, all you need to do is create a check type that 
implements the PreCheck interface.

```
type foo struct {}
...
func (f *foo) Run() error
```

Then add that check type you've created to the input list of PreChecks for
the `RunChecks` method [here](/legacy/dockerregistry/inventory.go).

Note that the `Run` method of the precheck interface does not accept any 
parameters, so any information that you need for your check should be passed
into the check type as a field. For example, if you are running a check over 
promotion edges, then you can set up your check like so:

```
type foo struct {
	PromotionEdges map[PromotionEdge]interface{}
}
```
This way, in your check's `Run` function you can access the PromotionEdges as 
a field of your check.

```
func (f * foo) Run() error {
	edges := foo.PromotionEdges
}
```

## Current Checks
### ImageRemovalCheck
Images that have been promoted are pushed to production; and once pushed to 
production, they should never be removed. The `ImageRemovalCheck` checks if 
any images are removed in the pull request by comparing the state of the 
promoter manifests in the pull request's branch to the default development
branch. Two sets of Promotion Edges are generated (one for both the default
development branch and pull request) and then compared to make sure that every
destination image (defined by its image tag and digest) found in the default
development branch is found in the pull request.

This method for detecting removed images should ensure that pull requests are 
only rejected if an image is completely removed from production, while still 
allowing edge cases. One example edge case is where a user has already 
promoted an image foo from registry A to registry B. Then in a later pull 
request, the user promotes the same image foo from registry C to registry B. 
Although image foo is removed from registry A, this pull request should be 
accepted because the same image is still being promoted, albeit from a new 
location. 

### ImageSizeCheck
The larger an image is, the more likely it is to be a security risk; and in 
general, it is bad practice to use unnecessarily large images. The 
`ImageSizeCheck` checks if any images to be promoted are larger than the 
defined maximum image size. The api calls that get image information from GCR 
also give information on the image size in bytes. These image sizes are 
recorded and then checked to make sure they are under the maximum size 
defined by the *-max-image-size* in MiB. 

### ImageVulnerabilityCheck
Since promoted images are pushed to production and production images are 
effectively treated like the gold standard, it's important that we check 
all images for any vulnerabilities they might already have before promoting 
them. A vulnerability check also serves as a method for surfacing all 
vulnerabilities regardless if they have a fix to the user. To emphasize this 
point, the vulnerability check has been implemented in it's own separate Prow 
job [*pull-k8sio-cip-vuln*][k8sio-presubmits]
so that the check's logs (which will detail all the vulnerabilities that exist 
in the new images to be promoted) won't get mixed in with the logs from the 
promoter's other checks. 

The vulnerability check makes use of the Container Analysis API in order to:

1. scan all new staging images for vulnerabilities whenever they are added to
   an image staging project
2. get vulnerability information when we are checking the images to be promoted
   from a PR

To make use of this API, key pieces of infrastructure must be put in place, 
such as enabling the Container Analysis API on all image staging projects 
and authenticating the Prow job (pull-k8sio-cip-vuln) with a Google service
account that is authorized to access the vulnerability data for each 
staging project.

The vulnerability check will reject a pull request if it finds any 
vulnerabilities that are both beyond the severity threshold (defined by the 
*-vuln-severity-threshold*) and have a known fix; otherwise the check will 
accept the PR.

[add-prow-job]: https://git.k8s.io/test-infra/config/jobs/README.md#adding-or-updating-jobs
[k8sio-presubmits]: https://git.k8s.io/test-infra/config/jobs/kubernetes/sig-k8s-infra/releng/artifact-promotion-presubmits.yaml
