import type { Config } from "tailwindcss";

export const tokens: {
  colors: Record<string, string | Record<string, string>>;
  fontFamily: Record<string, string[]>;
};

export const theme: NonNullable<Config["theme"]>;
