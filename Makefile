BINARY=ecs-template
GOPATH := $(shell go env | grep GOPATH | sed 's/GOPATH="\(.*\)"/\1/')

release: clean clean-cache ## Generate a release, but don't publish to GitHub.
	goreleaser --skip-validate --skip-publish --snapshot

compress: ## Uses upx to compress release binaries (if installed, uses all cores/parallel comp.)
	(find dist/*/* | xargs -I{} -n1 upx "{}") || echo "not using upx for binary compression"

clean: ## Cleans up generated files/folders from the build.
	/bin/rm -rfv "dist" "public/dist" "${BINARY}"

clean-cache: ## Cleans up generated cache (speeds up during dev time).
	/bin/rm -rfv "public/.cache"
