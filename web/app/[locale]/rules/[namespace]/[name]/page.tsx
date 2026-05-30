import { ComponentDetail } from "@/components/component-detail";

export default async function RuleDetailPage({
  params,
}: {
  params: Promise<{ namespace: string; name: string }>;
}) {
  const { namespace, name } = await params;
  return <ComponentDetail kind="rule" namespace={namespace} name={name} basePath="/rules" />;
}
