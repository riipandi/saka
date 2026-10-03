import { Hr } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface ApiKeyExpiringData {
  name: string
  apiKeyName: string
  expiresAt: string
}

interface ApiKeyExpiringEmailProps {
  logoURL: string
  appName: string
  data: ApiKeyExpiringData
}

export const ApiKeyExpiringEmail = ({ logoURL, appName, data }: ApiKeyExpiringEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='API Key Expiring Soon' warning />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.name},</Text>

      <Text>
        This is a reminder that your API key <strong>{data.apiKeyName}</strong> will expire soon.
      </Text>

      <Text>
        Expiration date: <strong>{data.expiresAt}</strong>
      </Text>

      <Text>Please generate a new API key if you need continued access.</Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

ApiKeyExpiringEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    apiKeyName: '{{.Data.APIKeyName}}',
    // The sender pre-formats the date: template data travels the task
    // queue as JSON, where a time.Time would arrive as a string.
    expiresAt: '{{.Data.ExpiresAt}}'
  }
}

ApiKeyExpiringEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Elias Schneider',
    apiKeyName: 'My API Key',
    expiresAt: 'September 30, 2024'
  }
}

export default ApiKeyExpiringEmail
