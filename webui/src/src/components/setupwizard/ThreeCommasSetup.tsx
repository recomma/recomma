import { useState } from 'react';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { Label } from '../ui/label';
import { Textarea } from '../ui/textarea';
import { Alert, AlertDescription } from '../ui/alert';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '../ui/dialog';
import { HelpCircle, Upload, FileText, AlertCircle } from 'lucide-react';

interface ThreeCommasSetupProps {
  onComplete: (data: { apiKey: string; privateKeyFile: string }) => void;
  onBack: () => void;
}

export function ThreeCommasSetup({ onComplete, onBack }: ThreeCommasSetupProps) {
  const [apiKey, setApiKey] = useState('');
  const [privateKeyFile, setPrivateKeyFile] = useState('');
  const [uploadMethod, setUploadMethod] = useState<'paste' | 'upload'>('paste');
  const [error, setError] = useState<string | null>(null);

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) {
      const reader = new FileReader();
      reader.onload = (event) => {
        const content = event.target?.result as string;
        setPrivateKeyFile(content);
      };
      reader.readAsText(file);
    }
  };

  const handleSubmit = () => {
    setError(null);

    if (!apiKey.trim()) {
      setError('API Key is required');
      return;
    }

    if (!privateKeyFile.trim()) {
      setError('Private Key file content is required');
      return;
    }

    // Basic PEM format validation
    if (!privateKeyFile.includes('BEGIN') || !privateKeyFile.includes('END')) {
      setError('Private key file does not appear to be in valid PEM format');
      return;
    }

    onComplete({ apiKey, privateKeyFile });
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="text-slate-900">3Commas API Credentials</h3>
        <Dialog>
          <DialogTrigger asChild>
            <button className="inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md border border-slate-200 bg-white px-3 py-1 transition-colors hover:bg-slate-100 hover:text-slate-900">
              <HelpCircle className="w-4 h-4" />
              How to get these?
            </button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Getting 3Commas API Credentials</DialogTitle>
              <DialogDescription className="space-y-4 pt-4">
                <div>
                  <h4 className="text-slate-900 mb-2">Instructions will be added here</h4>
                  <p className="text-slate-600">
                    Detailed steps on how to:
                  </p>
                  <ul className="list-disc list-inside space-y-1 mt-2 text-slate-600">
                    <li>Access your 3Commas account settings</li>
                    <li>Generate an API key</li>
                    <li>Download or create the private key PEM file</li>
                    <li>Set proper permissions for the API key</li>
                  </ul>
                </div>
              </DialogDescription>
            </DialogHeader>
          </DialogContent>
        </Dialog>
      </div>

      <div className="space-y-2">
        <Label htmlFor="apiKey">3Commas API Key</Label>
        <Input
          id="apiKey"
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          placeholder="Enter your 3Commas API key"
        />
      </div>

      <div className="space-y-3">
        <Label>Private Key File (PEM format)</Label>
        
        <div className="flex gap-2">
          <Button
            type="button"
            variant={uploadMethod === 'paste' ? 'default' : 'outline'}
            size="sm"
            onClick={() => setUploadMethod('paste')}
            className="flex-1"
          >
            <FileText className="w-4 h-4 mr-2" />
            Paste Content
          </Button>
          <Button
            type="button"
            variant={uploadMethod === 'upload' ? 'default' : 'outline'}
            size="sm"
            onClick={() => setUploadMethod('upload')}
            className="flex-1"
          >
            <Upload className="w-4 h-4 mr-2" />
            Upload File
          </Button>
        </div>

        {uploadMethod === 'paste' ? (
          <Textarea
            value={privateKeyFile}
            onChange={(e) => setPrivateKeyFile(e.target.value)}
            placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
            rows={8}
            className="font-mono text-sm"
          />
        ) : (
          <div className="border-2 border-dashed border-slate-200 rounded-lg p-8 text-center">
            <input
              type="file"
              id="pemFile"
              accept=".pem,.key,.txt"
              onChange={handleFileUpload}
              className="hidden"
            />
            <label htmlFor="pemFile" className="cursor-pointer">
              <Upload className="w-8 h-8 text-slate-400 mx-auto mb-2" />
              <p className="text-slate-600 mb-1">
                Click to upload PEM file
              </p>
              <p className="text-slate-400 text-sm">
                or drag and drop
              </p>
            </label>
            {privateKeyFile && (
              <p className="text-green-600 text-sm mt-3">
                ✓ File loaded ({privateKeyFile.length} characters)
              </p>
            )}
          </div>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="flex gap-3">
        <Button variant="outline" onClick={onBack} className="flex-1">
          Back
        </Button>
        <Button onClick={handleSubmit} className="flex-1">
          Continue
        </Button>
      </div>
    </div>
  );
}
