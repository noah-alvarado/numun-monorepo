// Astro content collection schemas — mirror docs/subsystems/CMS_CONTENT_MODEL.md §4.
//
// All collections load from the repo-root `/content/` directory (one level
// above `/site/`) so Decap CMS writes to the same files Astro reads. This
// matches CMS_CONTENT_MODEL.md §2 (content directory layout).
//
// Notes for future editors:
//   - Image references are plain strings (URL paths) rather than Astro's
//     `image()` helper, because Decap writes paths like `/uploads/foo.jpg`
//     and we don't want the build to fail just because a binary isn't in
//     the repo yet. Build-time alt-text/broken-link checks belong in the
//     build script (CMS_CONTENT_MODEL.md §10), not the schema.
//   - Singleton pages each get their own one-entry collection — type-safe
//     and easy to `getEntry()` by name.

import { defineCollection } from "astro:content";
import { z } from "astro/zod";
import { glob } from "astro/loaders";

// Resolve to /content/<sub> from the site/src/content/config.ts location.
const root = (sub: string) => `../content/${sub}`;

// ── shared sub-schemas ──────────────────────────────────────────────────────

const seo = z
  .object({
    title: z.string().max(70).optional(),
    description: z.string().max(200).optional(),
    ogImage: z.string().optional(),
    noindex: z.boolean().default(false),
  })
  .optional();

const image = z.object({
  src: z.string(),
  alt: z.string(),
});

const ctaShape = z.object({
  label: z.string().max(30),
  href: z.string(),
});

// Hero primary CTA: href is optional. Empty / unset → renderer falls back to
// the env-appropriate portal URL (site/src/lib/site-urls.ts).
const heroPrimaryCtaShape = z.object({
  label: z.string().max(30),
  href: z.string().optional(),
});

// ── singletons under /content/pages/ ────────────────────────────────────────

const home = defineCollection({
  loader: glob({ pattern: "home.md", base: root("pages") }),
  schema: z.object({
    hero: z.object({
      headline: z.string().max(100),
      subheadline: z.string().max(200),
      image,
      primaryCta: heroPrimaryCtaShape,
      secondaryCta: ctaShape.optional(),
    }),
    intro: z.object({
      headline: z.string().max(100),
      body: z.string(),
    }),
    featuredSections: z
      .array(
        z.object({
          label: z.string().max(80),
          summary: z.string().max(200),
          image,
          ctaLabel: z.string().max(30),
          ctaHref: z.string(),
        }),
      )
      .default([]),
    currentConferenceTeaser: z
      .object({
        overrideHeadline: z.string().max(100).optional(),
        overrideCtaLabel: z.string().max(30).optional(),
      })
      .optional(),
    seo,
  }),
});

const about = defineCollection({
  loader: glob({ pattern: "about.md", base: root("pages") }),
  schema: z.object({
    title: z.string(),
    heroImage: image.optional(),
    mission: z.string().max(300),
    vision: z.string().max(300),
    seo,
  }),
});

const hostedConference = defineCollection({
  loader: glob({ pattern: "hosted-conference.md", base: root("pages") }),
  schema: z.object({
    title: z.string(),
    tagline: z.string().max(200),
    forAdvisorsBody: z.string().optional(),
    forDelegatesBody: z.string().optional(),
    // Optional override. When empty, the page renders the env-appropriate
    // portal URL (https://portal.<apex>) computed from Astro.site at build
    // time. Keeping the default out of the schema avoids baking the wrong
    // origin into prod or test content trees.
    registrationPortalUrl: z.string().optional(),
    seo,
  }),
});

const travelTeam = defineCollection({
  loader: glob({ pattern: "travel-team.md", base: root("pages") }),
  schema: z.object({
    title: z.string(),
    conferencesAttended: z
      .array(
        z.object({
          conferenceName: z.string(),
          location: z.string(),
          date: z.coerce.date(),
          summary: z.string().max(200),
          highlights: z.array(z.string()).optional(),
        }),
      )
      .default([]),
    seo,
  }),
});

const resources = defineCollection({
  loader: glob({ pattern: "resources.md", base: root("pages") }),
  schema: z.object({
    title: z.string(),
    intro: z.string().optional(),
    linkGroups: z
      .array(
        z.object({
          groupTitle: z.string().max(80),
          links: z.array(
            z.object({
              label: z.string().max(80),
              href: z.string(),
              description: z.string().max(200).optional(),
            }),
          ),
        }),
      )
      .default([]),
    seo,
  }),
});

const contact = defineCollection({
  loader: glob({ pattern: "contact.md", base: root("pages") }),
  schema: z.object({
    title: z.string(),
    generalEmail: z.email(),
    inquiryRouting: z
      .array(
        z.object({
          label: z.string().max(80),
          email: z.email(),
        }),
      )
      .optional(),
    seo,
  }),
});

// ── folder collections ──────────────────────────────────────────────────────

const leadership = defineCollection({
  loader: glob({ pattern: "**/*.md", base: root("leadership") }),
  schema: z.object({
    name: z.string().max(80),
    role: z.string().max(80),
    roleCategory: z.enum(["secretariat", "executive-board", "staff"]),
    year: z.number().int(),
    order: z.number().int(),
    headshot: image,
    email: z.email().optional(),
    linkedIn: z.url().optional(),
  }),
});

const news = defineCollection({
  loader: glob({ pattern: "**/*.md", base: root("news") }),
  schema: z.object({
    title: z.string().max(120),
    slug: z.string().optional(),
    date: z.coerce.date(),
    author: z.string().max(80).optional(),
    summary: z.string().max(200),
    heroImage: image.optional(),
    tags: z.array(z.string()).optional(),
    seo,
  }),
});

const pastConferences = defineCollection({
  loader: glob({ pattern: "**/*.md", base: root("past-conferences") }),
  schema: z.object({
    conferenceName: z.string().max(80),
    editionNumber: z.number().int(),
    year: z.number().int(),
    dateRange: z.string().max(80),
    heroImage: image.optional(),
    photoGallery: z
      .array(
        z.object({
          image,
          caption: z.string().max(200).optional(),
        }),
      )
      .optional(),
    notableAwards: z
      .array(
        z.object({
          awardName: z.string().max(80),
          recipient: z.string().max(120),
          kind: z.enum(["delegate", "delegation"]),
        }),
      )
      .optional(),
    seo,
  }),
});

// Awards archive — API-synced by the Lambdalith from DDB as of M11. Schema
// mirrors /cms/config.yml; the API writes the frontmatter and any manual
// edits via Decap get overwritten on the next AwardService mutation.
const awardsArchive = defineCollection({
  loader: glob({ pattern: "**/*.md", base: root("awards-archive") }),
  schema: z.object({
    awardId: z.string(),
    conferenceId: z.string().optional(),
    year: z.number().int().optional(),
    awardName: z.string().max(200),
    category: z.string().max(200).optional(),
    recipients: z
      .array(
        z.object({
          kind: z.enum([
            "delegate",
            "delegation",
            "committee",
            "user",
            "conference",
          ]),
          id: z.string(),
          displayName: z.string().optional(),
        }),
      )
      .default([]),
    awardedAt: z.coerce.date().optional(),
    awardedBy: z.string().optional(),
  }),
});

const backgroundGuides = defineCollection({
  loader: glob({ pattern: "**/*.md", base: root("background-guides") }),
  schema: z.object({
    committeeName: z.string().max(80),
    committeeType: z.enum(["crisis", "non-crisis"]),
    committeeSize: z.enum(["small", "medium", "large"]),
    conferenceName: z.string().max(80),
    pdfFile: z.string(),
    updatedAt: z.coerce.date().optional(),
  }),
});

const faq = defineCollection({
  loader: glob({ pattern: "**/*.md", base: root("faq") }),
  schema: z.object({
    question: z.string().max(200),
    category: z.enum([
      "general",
      "registration",
      "payment",
      "day-of",
      "delegates",
      "advisors",
    ]),
    order: z.number().int(),
  }),
});

// ── /content/config singletons ──────────────────────────────────────────────

const seoDefaults = defineCollection({
  loader: glob({ pattern: "seo-defaults.md", base: root("config") }),
  schema: z.object({
    siteTitle: z.string(),
    siteDescription: z.string().max(200),
    defaultOgImage: z.string().optional(),
    twitterHandle: z.string().optional(),
    themeColor: z.string().optional(),
  }),
});

const footer = defineCollection({
  loader: glob({ pattern: "footer.md", base: root("config") }),
  schema: z.object({
    copyrightText: z.string().max(200),
    legalLinks: z
      .array(z.object({ label: z.string().max(40), href: z.string() }))
      .min(1, "At least one legal link is required."),
    sponsorLogos: z
      .array(
        z.object({
          name: z.string().max(80),
          image,
          href: z.string().optional(),
        }),
      )
      .optional(),
    acknowledgments: z.string().optional(),
  }),
});

const contactLinks = defineCollection({
  loader: glob({ pattern: "contact-links.md", base: root("config") }),
  schema: z.object({
    socialLinks: z
      .array(
        z.object({
          platform: z.enum([
            "instagram",
            "twitter",
            "linkedin",
            "facebook",
            "youtube",
            "other",
          ]),
          url: z.string(),
          label: z.string().max(40).optional(),
        }),
      )
      .optional(),
    primaryEmail: z.email(),
    mailingAddress: z.string().max(300).optional(),
  }),
});

// ── collection registry ─────────────────────────────────────────────────────

export const collections = {
  home,
  about,
  "hosted-conference": hostedConference,
  "travel-team": travelTeam,
  resources,
  contact,
  leadership,
  news,
  "past-conferences": pastConferences,
  "awards-archive": awardsArchive,
  "background-guides": backgroundGuides,
  faq,
  "seo-defaults": seoDefaults,
  footer,
  "contact-links": contactLinks,
};
