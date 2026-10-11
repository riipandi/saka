import { createFileRoute } from '@tanstack/react-router'
import { ForgotPasswordView } from './-forgot-password-view'

export const Route = createFileRoute('/(auth)/forgot-password')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Forgot Password'
  }
})

function RouteComponent() {
  return <ForgotPasswordView />
}
