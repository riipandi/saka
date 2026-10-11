import { createFileRoute } from '@tanstack/react-router'
import { UserDetailView } from './-user-detail-view'

export const Route = createFileRoute('/(app)/admin/users/$id')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'User'
  }
})

function RouteComponent() {
  const { id } = Route.useParams()
  return <UserDetailView userId={id} />
}
