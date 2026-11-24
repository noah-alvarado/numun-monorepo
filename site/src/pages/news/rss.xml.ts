import rss from "@astrojs/rss";
import { getCollection } from "astro:content";
import type { APIContext } from "astro";

export async function GET(context: APIContext) {
  const posts = await getCollection("news");
  const sorted = [...posts].sort(
    (a, b) => b.data.date.getTime() - a.data.date.getTime(),
  );

  return rss({
    title: "NUMUN News",
    description:
      "Announcements and updates from Northwestern University Model United Nations.",
    site: context.site ?? "https://numun.org",
    items: sorted.map((p) => ({
      title: p.data.title,
      pubDate: p.data.date,
      description: p.data.summary,
      link: `/news/${p.id}/`,
    })),
  });
}
