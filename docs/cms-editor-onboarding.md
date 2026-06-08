# CMS editor onboarding

For new NUMUN content editors — typically incoming board members at annual leadership turnover. You'll edit the public NUMUN landing site through a friendly web UI; no command-line tools needed.

This guide assumes you already have a GitHub account. If not, sign up at https://github.com first.

---

## 1. Get added as an editor

1. Email your **GitHub username** (not your email — your `@handle`) to a current NUMUN admin.
2. The admin adds you as a collaborator on the content repository with **Write** permission. You'll receive a GitHub invitation email; accept it.
3. Once accepted, you're ready to log in.

---

## 2. Log in to the CMS

1. Open **https://cms.numun.org** in your browser.
2. Click **Login with GitHub**. A popup window appears.
3. The first time you log in, GitHub asks you to authorize the "NUMUN CMS" application. Click **Authorize**.
4. The popup closes and you land in the editor.

If the popup never closes or the editor doesn't load, see [Troubleshooting](#5-troubleshooting).

---

## 3. Editing content

The left sidebar lists every section of the site you can edit:

| Section | What it is |
|---|---|
| **Pages** | The fixed landing pages (Home, About, Hosted Conference, Travel Team, Resources, Contact). |
| **Leadership** | Executive board members. One entry per person per academic year. |
| **News** | Blog-style posts. |
| **Past Conferences** | Recap entries for completed conferences. |
| **Awards Archive** | Curated highlights, one per notable award per year. |
| **Background Guides** | Per-committee BG-guide metadata + PDF upload. |
| **FAQ** | Frequently-asked questions. |
| **Config** | Site-wide settings: SEO defaults, footer, contact links. |

To edit:

1. Click a section.
2. Pick an existing entry, or click **New** to add one.
3. Fill in the fields. Required fields are marked.
4. Images: drag-drop into the upload widget. Every image **requires alt text** — describe the image in one sentence; this is for accessibility and search.
5. Click **Publish** (top right). Your change is committed to the site repository immediately.
6. A **GitHub Actions** build kicks off automatically — within ~2 minutes your change will appear on https://numun.org.

---

## 4. Recovering from a mistake

### Undo your last edit

1. Open the repository on GitHub: **https://github.com/noah-alvarado/numun-monorepo**.
2. Find the file you edited under `content/`.
3. Click **History**, find your commit, and click **Revert** (or ask an admin to do it).
4. A new commit reverses your change. The site rebuilds automatically.

### Restore a deleted entry

Same as above — the entry's file lives in `content/<section>/`, and reverting the deletion commit brings it back.

### Image didn't upload, or I uploaded the wrong one

Re-upload the correct image in the same field, then **Publish** again. The old upload stays in the repo but is no longer referenced.

### The site looks broken after my edit

Edit the page again to fix it. If you can't, ask an admin to revert via GitHub.

---

## 5. Troubleshooting

### The login popup never closes

- Check your browser's pop-up blocker — allow popups for `cms.numun.org`.
- Make sure third-party cookies aren't blocked for `api.numun.org`. The OAuth proxy needs to set a short-lived cookie.
- Try in a private/incognito window.

### "Repository not found" or "Permission denied"

You're not yet a collaborator, or you didn't accept the GitHub invitation email. Check https://github.com/noah-alvarado/numun-monorepo — you should see it in your repository list.

### My change isn't appearing on the site

The deploy takes 1–2 minutes. Check the build status at:
**https://github.com/noah-alvarado/numun-monorepo/actions** — look for the most recent "deploy site" run.

If the build failed, click in for the logs — usually a missing required field. Edit the offending entry, fix, and publish again.

### I can't see my change locally / on preview

There is no separate preview environment. The CMS edits commit directly to `main` and the site rebuilds. If you'd like a draft-and-review flow, raise it with an admin — it's a configuration change we can apply.

---

## 6. Who to contact

If something looks seriously broken or you're locked out, contact the current NUMUN webmaster (listed under Leadership → Webmaster in the CMS) or any board member with admin access.

---

## Reference

- The full content model — what fields each section has and why — is in [docs/subsystems/CMS_CONTENT_MODEL.md](./subsystems/CMS_CONTENT_MODEL.md).
- The Decap CMS user guide: https://decapcms.org/docs/intro/.
