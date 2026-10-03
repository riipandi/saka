import { Hr } from 'react-email'
import { Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface SignupAttemptNoticeData {
  name: string
  email: string
}

interface SignupAttemptNoticeProps {
  logoURL: string
  appName: string
  data: SignupAttemptNoticeData
}

export const SignupAttemptNotice = ({ logoURL, appName, data }: SignupAttemptNoticeProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Sign-up Attempt for Your Email' />
      <Hr style={{ marginTop: '16px' }} />
      <Text>Hello {data.name},</Text>

      <Text>
        Someone just tried to create an account using <strong>{data.email}</strong> — the address
        already on an account in {appName}.
      </Text>

      <Alert variant='info' style={{ marginTop: '20px' }}>
        <strong>No action is needed.</strong> Nothing was created, nothing was changed, and this
        message grants nothing.
      </Alert>

      <Text>
        If you tried to sign up just now: you already have an account here — sign in instead, or
        reset your password if you have lost it.
      </Text>

      <Text>
        If this wasn&apos;t you, no response is required. We&apos;ll keep notifying you of attempts
        on your address.
      </Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

SignupAttemptNotice.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    email: '{{.Data.Email}}'
  }
}

SignupAttemptNotice.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Sophie Neveu',
    email: 'user@example.com'
  }
}

export default SignupAttemptNotice
