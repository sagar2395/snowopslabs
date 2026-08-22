# Developer Certificate of Origin

SnowOps Labs uses the Developer Certificate of Origin (DCO) — the same
lightweight mechanism the Linux kernel and many CNCF projects use. It replaces a
CLA: there is no separate agreement to sign and no bot account to authorize.

To contribute, add a `Signed-off-by` line to every commit — Git does this for
you with the `-s` flag:

```bash
git commit -s -m "fix: correct the teardown timeout"
```

This adds a line using your Git `user.name` and `user.email`:

```
Signed-off-by: Jane Doe <jane@example.com>
```

By adding that line you certify the following (DCO version 1.1):

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

Forgot to sign off? Amend the last commit with `git commit --amend -s`, or for a
whole branch: `git rebase --signoff main`.
