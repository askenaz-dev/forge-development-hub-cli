import { ComponentCatalog } from "@/components/component-catalog";

export default async function HooksPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  const { q } = await searchParams;
  return <ComponentCatalog kind="hook" basePath="/hooks" q={q ?? ""} />;
}
