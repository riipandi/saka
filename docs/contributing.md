# Contributing Guide

When contributing to this repository, please first discuss the change you wish to make via issue, email,
or any other method with the owners of this repository before making any changes. This way we can guide
you through the process and give feedback.

## Pull Request Process

You can contribute changes to this repo by opening a pull request:

1. After forking this repository to your Git account, make the proposed changes on your forked branch.
2. Run tests and linting locally.
3. Commit your changes and push them to your forked repository.
4. Navigate to the main project repository and select the _Pull Requests_ tab.
5. Click the _New pull request_ button, then select the option "Compare across forks"
6. Leave the base branch set to main. Set the compare branch to your forked branch, and open the pull request.
7. Once your pull request is created, ensure that all checks have passed and that your branch has no conflicts with the base branch. If there are any issues, resolve these changes in your local repository, and then commit and push them to git.
8. Similarly, respond to any reviewer comments or requests for changes by making edits to your local repository and pushing them to Git.
9. Once the pull request has been reviewed, those with write access to the branch will be able to merge your changes into the project repository.

If you need more information on the steps to create a pull request, you can find a detailed walkthrough in the [Github documentation][pull-requests-docs].

## Quick Start

Requirements: Go 1.27+, Node 24.21+, pnpm, and Docker (for the dev
containers).

```sh
pnpm install              # frontend dependencies
task deps                 # Go toolchain binaries (golangci-lint, goose, …)
task config:generate      # write app.config.json, then fill in the secrets
cp .env.example .env.local && edit .env.local
task compose:up           # postgres, mailpit, and the dev stack
task db:initialize        # migrations + system seed + first administrator
task dev                  # the dev loop: Vite + HMR behind the Go proxy on :3080
```

Sign in as the administrator `initialize` created, then make yourself at
home:

```sh
task lint                 # all linters (Go + JS)
task test                 # the test suites
task check                # vet + migrations + formatting
```

Read [the deployment guide](deployment.md) before serving a real
deployment, and [the product guide](product-guide.md) for what is already
built in.

[pull-requests-docs]: https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/proposing-changes-to-your-work-with-pull-requests/creating-a-pull-request-from-a-fork
