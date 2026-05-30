import { ComponentDetail } from "@/components/component-detail";

export default async function HookDetailPage({
  params,
}: {
  params: Promise<{ namespace: string; name: string }>;
}) {
  const { namespace, name } = await params;
  return <ComponentDetail kind="hook" namespace={namespace} name={name} basePath="/hooks" />;
}
