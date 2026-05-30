import { ComponentCatalog } from "@/components/component-catalog";

export default async function RulesPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  const { q } = await searchParams;
  return <ComponentCatalog kind="rule" basePath="/rules" q={q ?? ""} />;
}
