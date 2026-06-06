# CMS OAuth app setup

**Status:** stub. Filled out in M5.

How to register the GitHub OAuth App that backs Decap CMS auth: callback URL `https://api.numun.org/cms-oauth/callback`, homepage `https://cms.numun.org`, requested scope `repo`. Client id + secret stored as SSM `SecureString` parameters `/numun/prod/cms_oauth/client_id` and `/cms_oauth/client_secret`. See CMS_CONTENT_MODEL.md §8.3 and SECURITY.md §4.6.
