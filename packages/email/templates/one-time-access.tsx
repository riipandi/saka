import { Hr, CodeInline } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface OneTimeAccessData {
  name: string
  code: string
  expirationString: string
}

interface OneTimeAccessEmailProps {
  logoURL: string
  appName: string
  data: OneTimeAccessData
}

export const OneTimeAccessEmail = ({ logoURL, appName, data }: OneTimeAccessEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Your Login Code' />
      <Hr style={{ marginTop: '16px' }} />

      <Text style={{ marginTop: '18px' }}>
        Enter this one-time code on the sign-in screen to sign in to {appName}. The code works once.
      </Text>

      <Text style={{ textAlign: 'center' }}>
        <CodeInline style={{ fontSize: '24px', letterSpacing: '4px' }}>{data.code}</CodeInline>
      </Text>

      <Hr style={{ marginTop: '24px' }} />

      <Text style={{ marginTop: '24px' }}>
        <strong>Important:</strong> This code will expire in {data.expirationString}.
      </Text>

      <Text>If you did not make this request, please ignore this email.</Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

OneTimeAccessEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    code: '{{.Data.Code}}',
    expirationString: '{{.Data.ExpirationString}}'
  }
}

OneTimeAccessEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    code: 'GRYFFINDOR',
    expirationString: '15 minutes'
  }
}

export default OneTimeAccessEmail
