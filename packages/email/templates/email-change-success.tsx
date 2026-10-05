import { Hr } from 'react-email'
import { Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface EmailChangeSuccessData {
  name: string
  newEmail: string
}

interface EmailChangeSuccessProps {
  logoURL: string
  appName: string
  data: EmailChangeSuccessData
}

export const EmailChangeSuccess = ({ logoURL, appName, data }: EmailChangeSuccessProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Email Change Successful' />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.name},</Text>

      <Text>
        Your email address has been successfully changed to <strong>{data.newEmail}</strong>.
      </Text>

      <Alert variant='success'>
        <strong>Security Notice:</strong> All your sessions have been terminated for security.
        <br /> Please sign in again with your new email address.
      </Alert>

      <Text>If you did not make this change, please contact support immediately.</Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

EmailChangeSuccess.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    newEmail: '{{.Data.NewEmail}}'
  }
}

EmailChangeSuccess.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'John Doe',
    newEmail: 'new@example.com'
  }
}

export default EmailChangeSuccess
