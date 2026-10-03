import { Hr } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface TestEmailData {
  email: string
}

interface TestEmailProps {
  logoURL: string
  appName: string
  data: TestEmailData
}

export default function TestEmail({ logoURL, appName, data }: TestEmailProps) {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='SMTP Test Successful' />
      <Hr style={{ marginTop: '16px' }} />

      <Text>This is a test email to verify that your SMTP configuration is working correctly.</Text>

      <Text>
        <strong>Recipient:</strong> {data.email}
      </Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          If you received this email, your mailer is properly configured.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

TestEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    email: 'dummy@example.com'
  }
}

TestEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    email: '{{.Email}}'
  }
}
