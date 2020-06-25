# CIP Checks Interface
The addition of the checks interface to the Container Image Promoter is meant 
to make it easy to add checks against pull requests affecting the promoter 
manifests. The interface allows engineers to add checks without worrying about 
any pre-existing checks and test their own checks individually, while also 
giving freedom as to what conditionals or tags might be necessary for the 
check to occur. Aditionally, using an interface means easy expandability of 
check requirements in the future.

## Interface Explanation
The `PreCheck` interface is implemented like so in the 
[types.go](https://github.com/kubernetes-sigs/k8s-container-image-promoter/blob/master/lib/dockerregistry/types.go) 
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
request attempts to modify the promoter manifests. Currently, only the 
[*pull-k8s-cip*](https://github.com/kubernetes/test-infra/blob/master/config/jobs/kubernetes/sig-release/cip/container-image-promoter.yaml) 
Prow job calls the `RunChecks` function and actually runs each check. But new 
Prow jobs can be [added](https://github.com/kubernetes/test-infra/blob/master/config/jobs/README.md#adding-or-updating-jobs) 
to run an individual check in the future if that check is large enough. 

### How To Add A Check
In order to add a check, all you need to do is create a check type that 
implements the PreCheck interface.

```
type foo struct {}
...
func (f *foo) Run() error
```
Then add that check type you've created to the input list of PreChecks for 
the RunChecks method, which is called in the 
[cip.go](https://github.com/kubernetes-sigs/k8s-container-image-promoter/blob/master/cip.go) 
file.

Note that the `Run` method of the precheck interface does not accept any 
paramaters, so any information that you need for your check should be passed 
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
promoter manifests in the pull request's branch to the master branch. Two sets 
of Promotion Edges are generated (one for both the master branch and pull 
request) and then compared to make sure that every destination image (defined 
by its image tag and digest) found in the master branch is found in the pull 
request.

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
defined by the *--max-image-size* in MiB. 
	
## Checks To Be Implemented
### ImageVulnerabilityCheck
Since promoted images are pushed to production and production images are 
effectively treated like the gold standard, it's important that we check 
all images for any vulnerabilities they might already have before promoting 
them.
